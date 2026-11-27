package connectors

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const browserUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

// SaveRemote streams a URL into the user's bucket. A known Content-Length uses a
// presigned PUT. An unknown length uses multipart. Nothing is kept on disk.
func SaveRemote(ctx context.Context, userID int64, rawURL, name string, progress func(written, total int64)) (string, int64, error) {
	c, err := Load(ctx, userID)
	if err != nil {
		return "", 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("User-Agent", browserUA)
	ApplyCookies(userID, rawURL, req.Header)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return "", 0, fmt.Errorf("download %s", resp.Status)
	}
	if name == "" {
		name = nameFromResponse(rawURL, resp)
	}
	ct := resp.Header.Get("Content-Type")
	size := resp.ContentLength
	if size >= 0 {
		link, err := c.presignStream(ctx, name, ct, resp.Body, size, progress)
		if err == nil {
			return link, size, nil
		}
		resp.Body.Close()
		return saveRemoteSDK(ctx, c, userID, rawURL, name, progress)
	}
	link, err := c.Put(ctx, name, ct, resp.Body, -1, progress)
	return link, 0, err
}

func saveRemoteSDK(ctx context.Context, c Conn, userID int64, rawURL, name string, progress func(written, total int64)) (string, int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("User-Agent", browserUA)
	ApplyCookies(userID, rawURL, req.Header)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return "", 0, fmt.Errorf("download %s", resp.Status)
	}
	size := resp.ContentLength
	ct := resp.Header.Get("Content-Type")
	if name == "" {
		name = nameFromResponse(rawURL, resp)
	}
	link, err := c.PutSDK(ctx, name, ct, resp.Body, size, progress)
	return link, size, err
}

func nameFromResponse(rawURL string, resp *http.Response) string {
	if cd := resp.Header.Get("Content-Disposition"); cd != "" {
		if i := strings.Index(strings.ToLower(cd), "filename="); i >= 0 {
			name := strings.Trim(cd[i+len("filename="):], ` ";`)
			if j := strings.Index(name, ";"); j >= 0 {
				name = name[:j]
			}
			name = strings.Trim(name, `"`)
			if name != "" {
				return name
			}
		}
	}
	u, err := url.Parse(rawURL)
	if err == nil {
		base := filepath.Base(u.Path)
		if base != "" && base != "/" && base != "." {
			return base
		}
	}
	return "download"
}

// SaveYtDlp runs yt-dlp into a temp dir. With S3 configured the file is uploaded
// and the temp dir removed. Without S3 the local path is returned.
func SaveYtDlp(ctx context.Context, userID int64, cookieDir, rawURL, name string, onLine func(string)) (link, local string, size int64, err error) {
	dir, err := os.MkdirTemp("", "ytdlp-*")
	if err != nil {
		return "", "", 0, err
	}
	keepLocal := false
	defer func() {
		if !keepLocal {
			_ = os.RemoveAll(dir)
		}
	}()

	file, err := runYtDlp(ctx, userID, cookieDir, rawURL, name, dir, onLine)
	if err != nil {
		return "", "", 0, err
	}
	st, err := os.Stat(file)
	if err != nil {
		return "", "", 0, err
	}
	size = st.Size()

	if _, err := Load(ctx, userID); err != nil {
		destDir := filepath.Join("/app/generated", "ytdlp")
		if mkErr := os.MkdirAll(destDir, 0755); mkErr != nil {
			keepLocal = true
			return "", file, size, nil
		}
		dest := filepath.Join(destDir, fmt.Sprintf("%d_%s", time.Now().UnixNano(), SafeName(filepath.Base(file))))
		if renameErr := os.Rename(file, dest); renameErr != nil {
			keepLocal = true
			return "", file, size, nil
		}
		return "", dest, size, nil
	}

	link, _, err = PutFile(ctx, userID, file, filepath.Base(file))
	return link, "", size, err
}

func runYtDlp(ctx context.Context, userID int64, cookieDir, rawURL, name, dir string, onLine func(string)) (string, error) {
	output := filepath.Join(dir, "%(title).180B.%(ext)s")
	if name = SafeName(name); name != "" && name != "file" {
		if filepath.Ext(name) == "" {
			output = filepath.Join(dir, name+".%(ext)s")
		} else {
			output = filepath.Join(dir, name)
		}
	}
	args := []string{
		"--newline", "--no-warnings", "--no-playlist",
		"--user-agent", browserUA,
		"--merge-output-format", "mp4/mkv",
		"-o", output,
	}
	cookies := ""
	if cookieDir != "" {
		cookies = NetscapeIn(cookieDir)
	}
	if cookies == "" {
		cookies = NetscapeCookies(userID)
	}
	if cookies != "" {
		args = append(args, "--cookies", cookies)
	}
	if strings.Contains(strings.ToLower(rawURL), ".m3u8") {
		args = append(args, "--concurrent-fragments", "16")
	}
	args = append(args, rawURL)

	cmd := exec.CommandContext(ctx, "yt-dlp", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return "", err
	}
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var last string
	for sc.Scan() {
		line := sc.Text()
		last = line
		if onLine != nil {
			onLine(line)
		}
	}
	if err := cmd.Wait(); err != nil {
		if last != "" {
			return "", fmt.Errorf("yt-dlp: %s", trim(last, 300))
		}
		return "", fmt.Errorf("yt-dlp: %w", err)
	}
	return newestFile(dir)
}

func newestFile(dir string) (string, error) {
	var best string
	var bestTime time.Time
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if strings.HasSuffix(p, ".part") || strings.HasSuffix(p, ".ytdl") {
			return nil
		}
		if info.ModTime().After(bestTime) && info.Size() > 0 {
			best = p
			bestTime = info.ModTime()
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if best == "" {
		return "", fmt.Errorf("yt-dlp produced no file")
	}
	return best, nil
}

func trim(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// SafeName keeps a filename portable.
func SafeName(name string) string {
	name = strings.TrimSpace(filepath.Base(name))
	if name == "" || name == "." || name == string(filepath.Separator) {
		return ""
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), "._")
	if len(out) > 120 {
		out = out[:120]
	}
	return out
}

// ParseKV reads key=value lines. The value may contain '='.
func ParseKV(text string) map[string]string {
	out := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		out[strings.ToLower(strings.TrimSpace(k))] = strings.TrimSpace(v)
	}
	return out
}

// ParseInt is used by progress lines.
func ParseInt(s string) int64 {
	n, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return n
}
