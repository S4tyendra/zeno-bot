package downloader

import (
	"context"
	"fmt"
	"html"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/amarnathcjd/gogram/telegram"
)

func UploadToTelegram(ctx context.Context, client *telegram.Client, task *DownloadTask, mode UploadMode, reporter *ThrottledReporter) error {
	filePath := task.FilePath
	if filePath == "" || !fileExists(filePath) || !isUnderDownloads(filePath) {
		return fmt.Errorf("file not found on disk: %s", filePath)
	}

	fi, err := os.Stat(filePath)
	if err != nil {
		return fmt.Errorf("stat failed: %w", err)
	}
	fileSize := fi.Size()
	fileName := filepath.Base(filePath)

	// Telegram MTProto file limit check (2000 MB)
	if fileSize > 2000*1024*1024 {
		return fmt.Errorf("file size (%s) exceeds Telegram 2GB limit", FormatBytes(fileSize))
	}

	log.Printf("[Downloader] Uploading task #%d (%s, %d bytes) to chat %d as %s", task.ID, filePath, fileSize, task.ChatID, mode)
	reporter.ForceReport(fmt.Sprintf("📤 <b>Starting Upload for Task #%d</b>\n\n📄 <b>File:</b> <code>%s</code>\n📦 <b>Size:</b> <code>%s</code>", task.ID, html.EscapeString(fileName), FormatBytes(fileSize)))

	mediaOpts := &telegram.MediaOptions{
		Caption:   fmt.Sprintf("📁 <b>%s</b>\n📦 <b>Size:</b> <code>%s</code>\n⚡ <i>Downloaded via Zeno</i>", html.EscapeString(fileName), FormatBytes(fileSize)),
		ParseMode: "HTML",
		Upload: &telegram.UploadOptions{
			FileName: fileName,
			Threads:  4,
			Ctx:      ctx,
			ProgressCallback: func(p *telegram.ProgressInfo) {
				reporter.Report(ProgressUpdate{
					Action:     "Uploading",
					FileName:   fileName,
					Current:    p.Current,
					Total:      fileSize,
					Speed:      p.CurrentSpeed,
					ETA:        p.ETA,
					Percentage: p.Percentage,
				})
			},
		},
	}

	if task.ReplyMsgID != 0 {
		mediaOpts.ReplyTo = &telegram.InputReplyToMessage{
			ReplyToMsgID: task.ReplyMsgID,
		}
	}

	ext := strings.ToLower(filepath.Ext(fileName))
	switch mode {
	case UploadDocument:
		mediaOpts.ForceDocument = true
	case UploadVideo:
		mediaOpts.ForceDocument = false
		mediaOpts.Attributes = []telegram.DocumentAttribute{
			&telegram.DocumentAttributeVideo{
				SupportsStreaming: true,
			},
		}
	case UploadAudio:
		mediaOpts.ForceDocument = false
		mediaOpts.Attributes = []telegram.DocumentAttribute{
			&telegram.DocumentAttributeAudio{
				Title: strings.TrimSuffix(fileName, ext),
			},
		}
	case UploadPhoto:
		mediaOpts.ForceDocument = false
	case UploadAuto:
		if isVideoExt(ext) {
			mediaOpts.ForceDocument = false
			mediaOpts.Attributes = []telegram.DocumentAttribute{
				&telegram.DocumentAttributeVideo{
					SupportsStreaming: true,
				},
			}
		} else if isAudioExt(ext) {
			mediaOpts.ForceDocument = false
			mediaOpts.Attributes = []telegram.DocumentAttribute{
				&telegram.DocumentAttributeAudio{
					Title: strings.TrimSuffix(fileName, ext),
				},
			}
		} else if isPhotoExt(ext) {
			mediaOpts.ForceDocument = false
		} else {
			mediaOpts.ForceDocument = true
		}
	}

	_, err = client.SendMedia(task.ChatID, filePath, mediaOpts)
	if err != nil {
		return fmt.Errorf("telegram upload failed: %w", err)
	}

	reporter.Finish(fmt.Sprintf("✅ <b>Task #%d Upload Completed!</b>\n\n📄 <code>%s</code> (%s)", task.ID, html.EscapeString(fileName), FormatBytes(fileSize)))

	return nil
}

func isVideoExt(ext string) bool {
	switch ext {
	case ".mp4", ".mkv", ".mov", ".webm", ".avi", ".flv", ".ts", ".m4v":
		return true
	}
	return false
}

func isAudioExt(ext string) bool {
	switch ext {
	case ".mp3", ".m4a", ".flac", ".wav", ".ogg", ".opus", ".aac", ".wma":
		return true
	}
	return false
}

func isPhotoExt(ext string) bool {
	switch ext {
	case ".jpg", ".jpeg", ".png", ".webp", ".bmp":
		return true
	}
	return false
}
