package downloader

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/amarnathcjd/gogram/telegram"
)

const (
	DefaultUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
)

// EnsureTaskDir creates and returns an isolated directory for a specific download task.
func EnsureTaskDir(taskID int64) (string, error) {
	dir := taskDir(taskID)
	err := os.MkdirAll(dir, 0755)
	return dir, err
}

// getRealisticHeaders builds modern browser headers, dynamically generating origin/referer from the URL.
func getRealisticHeaders(rawURL string) map[string]string {
	headers := map[string]string{
		"User-Agent":                DefaultUA,
		"Accept":                    "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8",
		"Accept-Language":           "en-US,en;q=0.9",
		"Sec-Ch-Ua":                 `"Google Chrome";v="131", "Chromium";v="131", "Not_A Brand";v="24"`,
		"Sec-Ch-Ua-Mobile":          "?0",
		"Sec-Ch-Ua-Platform":        `"Windows"`,
		"Sec-Fetch-Dest":            "document",
		"Sec-Fetch-Mode":            "navigate",
		"Sec-Fetch-Site":            "none",
		"Sec-Fetch-User":            "?1",
		"Upgrade-Insecure-Requests": "1",
	}

	if u, err := url.Parse(rawURL); err == nil && u.Host != "" {
		origin := fmt.Sprintf("%s://%s", u.Scheme, u.Host)
		headers["Origin"] = origin
		headers["Referer"] = origin + "/"
	}

	return headers
}

// DownloadTelegramMedia downloads media attached to a Telegram message into the task's directory.
func DownloadTelegramMedia(ctx context.Context, client *telegram.Client, taskID int64, chatID int64, msgID int32, customName string, reporter *ThrottledReporter) (string, int64, error) {
	taskDir, err := EnsureTaskDir(taskID)
	if err != nil {
		return "", 0, fmt.Errorf("failed to create task directory: %w", err)
	}

	msgs, err := client.GetMessages(chatID, &telegram.SearchOption{IDs: []int32{msgID}})
	if err != nil || len(msgs) == 0 {
		return "", 0, fmt.Errorf("failed to fetch message #%d: %v", msgID, err)
	}

	msg := msgs[0]
	if msg.Media() == nil {
		return "", 0, fmt.Errorf("replied message does not contain downloadable media")
	}

	originalName := getTelegramMediaFilename(&msg)
	fileName := sanitizeFileName(customName)
	if fileName != "" {
		fileName = preserveExt(fileName, originalName)
	} else {
		fileName = sanitizeFileName(originalName)
	}
	if fileName == "" {
		fileName = fmt.Sprintf("tg_%d_%d.dat", chatID, msgID)
	}

	targetPath := filepath.Join(taskDir, fileName)

	log.Printf("[Downloader] Starting TG download task #%d, msg #%d -> %s", taskID, msgID, targetPath)

	downloadOpts := &telegram.DownloadOptions{
		FileName: targetPath,
		Threads:  4,
		Ctx:      ctx,
		ProgressCallback: func(p *telegram.ProgressInfo) {
			reporter.Report(ProgressUpdate{
				Action:     "Downloading (TG)",
				FileName:   fileName,
				Current:    p.Current,
				Total:      p.TotalSize,
				Speed:      p.CurrentSpeed,
				ETA:        p.ETA,
				Percentage: p.Percentage,
			})
		},
	}

	savedPath, err := client.DownloadMedia(msg.Media(), downloadOpts)
	if err != nil {
		_ = os.Remove(targetPath)
		return "", 0, err
	}

	fi, err := os.Stat(savedPath)
	if err != nil {
		return savedPath, 0, nil
	}

	return savedPath, fi.Size(), nil
}

func getTelegramMediaFilename(msg *telegram.NewMessage) string {
	if msg.Message == nil || msg.Message.Media == nil {
		return ""
	}
	switch media := msg.Message.Media.(type) {
	case *telegram.MessageMediaPhoto:
		return fmt.Sprintf("photo_%d.jpg", msg.ID)
	case *telegram.MessageMediaDocument:
		if doc, ok := media.Document.(*telegram.DocumentObj); ok {
			var isVideo, isAudio bool
			for _, attr := range doc.Attributes {
				if fn, ok := attr.(*telegram.DocumentAttributeFilename); ok && fn.FileName != "" {
					return fn.FileName
				}
				if _, ok := attr.(*telegram.DocumentAttributeVideo); ok {
					isVideo = true
				}
				if _, ok := attr.(*telegram.DocumentAttributeAudio); ok {
					isAudio = true
				}
			}
			if isVideo {
				return fmt.Sprintf("video_%d.mp4", msg.ID)
			}
			if isAudio {
				return fmt.Sprintf("audio_%d.mp3", msg.ID)
			}
			return fmt.Sprintf("doc_%d.dat", msg.ID)
		}
	}
	return ""
}

var aria2Regex = regexp.MustCompile(`\[#\w+\s+([0-9.]+\w+)/([0-9.]+\w+)\((\d+)%\)\s+CN:\d+\s+DL:([0-9.]+\w+)(?:\s+ETA:(\w+))?`)

// DownloadAria2 downloads using aria2c with 16 connections, 16 splits, continue flag, and realistic headers.
func DownloadAria2(ctx context.Context, taskID int64, rawURL, customName string, reporter *ThrottledReporter) (string, int64, error) {
	taskDir, err := EnsureTaskDir(taskID)
	if err != nil {
		return "", 0, fmt.Errorf("failed to create task directory: %w", err)
	}

	args := []string{
		"-x", "16",
		"-s", "16",
		"-k", "1M",
		"-c",
		"--min-split-size=1M",
		"--max-connection-per-server=16",
		"--summary-interval=1",
		"--console-log-level=notice",
		"--download-result=hide",
		"--auto-file-renaming=false",
		"--allow-overwrite=true",
		"--dir=" + taskDir,
	}

	headers := getRealisticHeaders(rawURL)
	if ua, ok := headers["User-Agent"]; ok {
		args = append(args, "--user-agent="+ua)
		delete(headers, "User-Agent")
	}
	if ref, ok := headers["Referer"]; ok {
		args = append(args, "--referer="+ref)
		delete(headers, "Referer")
	}
	for k, v := range headers {
		args = append(args, fmt.Sprintf("--header=%s: %s", k, v))
	}

	outName := sanitizeFileName(customName)
	if outName != "" {
		outName = preserveExt(outName, filepath.Base(rawURL))
		args = append(args, "--out="+outName)
	}

	args = append(args, rawURL)

	err = runCancellable(ctx, "aria2c", args, func(line string) {
		matches := aria2Regex.FindStringSubmatch(line)
		if len(matches) < 4 {
			return
		}
		pct, _ := strconv.ParseFloat(matches[3], 64)
		etaStr := ""
		if len(matches) >= 6 && matches[5] != "" {
			etaStr = matches[5]
		}
		reporter.Report(ProgressUpdate{
			Action:     "Fast Download (aria2c)",
			FileName:   outName,
			Percentage: pct,
			RawStatus:  fmt.Sprintf("%s / %s | %s/s ETA: %s", matches[1], matches[2], matches[4], etaStr),
		})
	})
	if err != nil {
		if ctx.Err() != nil {
			return "", 0, ctx.Err()
		}
		return "", 0, fmt.Errorf("aria2c execution failed: %w", err)
	}

	return findCompletedFileInDir(taskDir)
}

// DownloadYtDlp downloads video/audio or m3u8 streams with realistic browser headers and HLS concurrency.
func DownloadYtDlp(ctx context.Context, taskID int64, rawURL, customName string, isM3U8 bool, reporter *ThrottledReporter) (string, int64, error) {
	taskDir, err := EnsureTaskDir(taskID)
	if err != nil {
		return "", 0, fmt.Errorf("failed to create task directory: %w", err)
	}

	outputTemplate := filepath.Join(taskDir, "%(title).200B.%(ext)s")
	outName := sanitizeFileName(customName)
	if outName != "" {
		if filepath.Ext(outName) == "" {
			outputTemplate = filepath.Join(taskDir, outName+".%(ext)s")
		} else {
			outputTemplate = filepath.Join(taskDir, outName)
		}
	}

	args := []string{
		"--newline",
		"--no-warnings",
		"--no-playlist",
		"--progress-template", "[download] %(progress._percent_str)s|%(progress._total_bytes_str|progress._total_bytes_estimate_str)s|%(progress._speed_str)s|%(progress._eta_str)s|%(progress.filename)s",
		"--user-agent", DefaultUA,
		"--merge-output-format", "mp4/mkv",
		"-o", outputTemplate,
	}

	headers := getRealisticHeaders(rawURL)
	if isM3U8 || isM3U8URL(rawURL) {
		headers["Accept"] = "application/vnd.apple.mpegurl,application/x-mpegURL,*/*"
		headers["Sec-Fetch-Dest"] = "empty"
		headers["Sec-Fetch-Mode"] = "cors"
		headers["Sec-Fetch-Site"] = "cross-site"
		args = append(args, "--concurrent-fragments", "16", "--downloader", "m3u8:native")
	}

	if ref, ok := headers["Referer"]; ok {
		args = append(args, "--referer", ref)
		delete(headers, "Referer")
	}
	delete(headers, "User-Agent")

	for k, v := range headers {
		args = append(args, "--add-header", fmt.Sprintf("%s:%s", k, v))
	}

	args = append(args, rawURL)

	log.Printf("[Downloader] Running yt-dlp for task #%d: %s", taskID, rawURL)
	err = runCancellable(ctx, "yt-dlp", args, func(line string) {
		if strings.HasPrefix(line, "[download] ") {
			parts := strings.Split(strings.TrimPrefix(line, "[download] "), "|")
			if len(parts) >= 5 {
				pctStr := strings.Trim(parts[0], " %")
				pct, _ := strconv.ParseFloat(pctStr, 64)
				sizeStr := strings.TrimSpace(parts[1])
				speedStr := strings.TrimSpace(parts[2])
				etaStr := strings.TrimSpace(parts[3])
				fname := filepath.Base(strings.TrimSpace(parts[4]))

				reporter.Report(ProgressUpdate{
					Action:     "Extracting Media (yt-dlp)",
					FileName:   fname,
					Percentage: pct,
					RawStatus:  fmt.Sprintf("%s | %s | ETA: %s", sizeStr, speedStr, etaStr),
				})
			}
			return
		}
		if strings.Contains(line, "[Merger]") || strings.Contains(line, "Merging formats into") {
			reporter.Report(ProgressUpdate{
				Action:     "Processing (Merging)",
				Percentage: 99.0,
				RawStatus:  "Merging video and audio streams...",
			})
		}
	})
	if err != nil {
		if ctx.Err() != nil {
			return "", 0, ctx.Err()
		}
		return "", 0, fmt.Errorf("yt-dlp failed: %w", err)
	}

	return findCompletedFileInDir(taskDir)
}

// DownloadHTTP streams a direct file over HTTP with standard browser headers.
func DownloadHTTP(ctx context.Context, taskID int64, rawURL, customName string, reporter *ThrottledReporter) (string, int64, error) {
	taskDir, err := EnsureTaskDir(taskID)
	if err != nil {
		return "", 0, fmt.Errorf("failed to create task directory: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
	if err != nil {
		return "", 0, err
	}

	headers := getRealisticHeaders(rawURL)
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	client := &http.Client{
		Timeout: 0,
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   30 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			TLSHandshakeTimeout:   30 * time.Second,
			ResponseHeaderTimeout: 60 * time.Second,
			IdleConnTimeout:       90 * time.Second,
			ForceAttemptHTTP2:     true,
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", 0, fmt.Errorf("server returned HTTP %s", resp.Status)
	}

	fileName := sanitizeFileName(customName)
	if fileName != "" {
		fileName = preserveExt(fileName, filepath.Base(req.URL.Path))
	}
	if fileName == "" {
		fileName = filenameFromDisposition(resp.Header.Get("Content-Disposition"))
	}
	if fileName == "" {
		fileName = sanitizeFileName(filepath.Base(req.URL.Path))
	}
	if fileName == "" {
		fileName = fmt.Sprintf("download_%d.dat", time.Now().Unix())
	}

	targetPath := filepath.Join(taskDir, fileName)
	out, err := os.Create(targetPath)
	if err != nil {
		return "", 0, err
	}
	defer out.Close()

	totalSize := resp.ContentLength
	var downloaded int64
	startTime := time.Now()
	buf := make([]byte, 64*1024)

	for {
		select {
		case <-ctx.Done():
			_ = os.Remove(targetPath)
			return "", 0, ctx.Err()
		default:
		}

		n, rErr := resp.Body.Read(buf)
		if n > 0 {
			_, wErr := out.Write(buf[:n])
			if wErr != nil {
				return "", 0, wErr
			}
			downloaded += int64(n)

			elapsed := time.Since(startTime).Seconds()
			speed := 0.0
			eta := 0.0
			pct := 0.0
			if elapsed > 0 {
				speed = float64(downloaded) / elapsed
			}
			if totalSize > 0 {
				pct = (float64(downloaded) / float64(totalSize)) * 100.0
				if speed > 0 {
					eta = float64(totalSize-downloaded) / speed
				}
			}

			reporter.Report(ProgressUpdate{
				Action:     "Downloading (HTTP)",
				FileName:   fileName,
				Current:    downloaded,
				Total:      totalSize,
				Speed:      speed,
				ETA:        eta,
				Percentage: pct,
			})
		}

		if rErr != nil {
			if rErr == io.EOF {
				break
			}
			return "", 0, rErr
		}
	}

	return targetPath, downloaded, nil
}

// findCompletedFileInDir finds the primary downloaded file in taskDir, ignoring temporary artifacts.
func findCompletedFileInDir(dir string) (string, int64, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", 0, err
	}

	var bestPath string
	var bestSize int64
	var latestMod time.Time

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(name, ".aria2") ||
			strings.HasSuffix(name, ".part") ||
			strings.HasSuffix(name, ".ytdl") ||
			strings.HasSuffix(name, ".tmp") {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		if info.Size() > bestSize {
			bestPath = filepath.Join(dir, name)
			bestSize = info.Size()
			latestMod = info.ModTime()
		} else if info.Size() > 0 && info.Size() == bestSize && info.ModTime().After(latestMod) {
			bestPath = filepath.Join(dir, name)
			latestMod = info.ModTime()
		}
	}

	if bestPath == "" {
		return "", 0, fmt.Errorf("no completed download file found in directory %s", dir)
	}
	return bestPath, bestSize, nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func runCancellable(ctx context.Context, name string, args []string, onLine func(string)) error {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		return err
	}

	pgid := cmd.Process.Pid
	done := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		buf := make([]byte, 0, 64*1024)
		scanner.Buffer(buf, 1024*1024)
		for scanner.Scan() {
			if onLine != nil {
				onLine(scanner.Text())
			}
		}
		done <- cmd.Wait()
	}()

	select {
	case <-ctx.Done():
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		<-done
		return ctx.Err()
	case err := <-done:
		return err
	}
}
