package downloader

import (
	"fmt"
	"html"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/amarnathcjd/gogram/telegram"
)

func FormatBytes(b int64) string {
	if b <= 0 {
		return "0 B"
	}
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	units := []string{"KB", "MB", "GB", "TB"}
	return fmt.Sprintf("%.2f %s", float64(b)/float64(div), units[exp])
}

func FormatSpeed(bytesPerSec float64) string {
	if bytesPerSec <= 0 {
		return "0 B/s"
	}
	const unit = 1024.0
	if bytesPerSec < unit {
		return fmt.Sprintf("%.1f B/s", bytesPerSec)
	} else if bytesPerSec < unit*unit {
		return fmt.Sprintf("%.1f KB/s", bytesPerSec/unit)
	} else if bytesPerSec < unit*unit*unit {
		return fmt.Sprintf("%.2f MB/s", bytesPerSec/(unit*unit))
	}
	return fmt.Sprintf("%.2f GB/s", bytesPerSec/(unit*unit*unit))
}

func FormatDuration(seconds float64) string {
	if seconds <= 0 || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
		return "00:00"
	}
	totalSecs := int(seconds)
	hours := totalSecs / 3600
	mins := (totalSecs % 3600) / 60
	secs := totalSecs % 60

	if hours > 0 {
		return fmt.Sprintf("%02d:%02d:%02d", hours, mins, secs)
	}
	return fmt.Sprintf("%02d:%02d", mins, secs)
}

func DrawProgressBar(percentage float64, length int) string {
	if percentage < 0 {
		percentage = 0
	}
	if percentage > 100 {
		percentage = 100
	}
	if length <= 0 {
		length = 10
	}

	filled := int((percentage / 100.0) * float64(length))
	if filled > length {
		filled = length
	}
	empty := length - filled

	filledChars := strings.Repeat("█", filled)
	emptyChars := strings.Repeat("░", empty)

	return fmt.Sprintf("[%s%s]", filledChars, emptyChars)
}

type ThrottledReporter struct {
	client      *telegram.Client
	chatID      int64
	msgID       int32
	taskID      int64
	taskType    TaskType
	lastEdit    time.Time
	lastText    string
	minInterval time.Duration
	isEditing   bool
	finalized   bool
	mu          sync.Mutex
}

func NewThrottledReporter(client *telegram.Client, chatID int64, msgID int32, taskID int64, taskType TaskType) *ThrottledReporter {
	return &ThrottledReporter{
		client:      client,
		chatID:      chatID,
		msgID:       msgID,
		taskID:      taskID,
		taskType:    taskType,
		minInterval: 2500 * time.Millisecond,
	}
}

func (tr *ThrottledReporter) Report(u ProgressUpdate) {
	tr.mu.Lock()
	if tr.finalized || tr.isEditing || tr.msgID == 0 {
		tr.mu.Unlock()
		return
	}

	now := time.Now()
	if now.Sub(tr.lastEdit) < tr.minInterval {
		tr.mu.Unlock()
		return
	}

	text := tr.formatUpdate(u)
	if text == tr.lastText {
		tr.mu.Unlock()
		return
	}

	tr.isEditing = true
	tr.lastEdit = now
	tr.lastText = text
	msgID := tr.msgID
	tr.mu.Unlock()

	go func() {
		defer func() {
			tr.mu.Lock()
			tr.isEditing = false
			tr.mu.Unlock()
		}()

		tr.mu.Lock()
		if tr.finalized {
			tr.mu.Unlock()
			return
		}
		tr.mu.Unlock()

		_, err := tr.client.EditMessage(tr.chatID, msgID, text, &telegram.SendOptions{ParseMode: "HTML"})
		if err != nil {
			errStr := strings.ToLower(err.Error())
			if strings.Contains(errStr, "flood_wait") {
				tr.mu.Lock()
				tr.minInterval = 5000 * time.Millisecond
				tr.mu.Unlock()
			}
		}
	}()
}

func (tr *ThrottledReporter) ForceReport(text string) {
	tr.forceEdit(text, nil, false)
}

func (tr *ThrottledReporter) Finish(text string) {
	tr.forceEdit(text, nil, true)
}

func (tr *ThrottledReporter) ForceReportWithMarkup(text string, markup telegram.ReplyMarkup) {
	tr.forceEdit(text, markup, true)
}

func (tr *ThrottledReporter) forceEdit(text string, markup telegram.ReplyMarkup, final bool) {
	tr.mu.Lock()
	if final {
		tr.finalized = true
	}
	tr.lastEdit = time.Now()
	tr.lastText = text
	msgID := tr.msgID
	tr.mu.Unlock()

	if final {
		for i := 0; i < 20; i++ {
			tr.mu.Lock()
			editing := tr.isEditing
			tr.mu.Unlock()
			if !editing {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
	}

	opts := &telegram.SendOptions{ParseMode: "HTML"}
	if markup != nil {
		opts.ReplyMarkup = markup
	}

	if msgID == 0 {
		sent, err := tr.client.SendMessage(tr.chatID, text, opts)
		if err == nil && sent != nil {
			tr.mu.Lock()
			tr.msgID = sent.ID
			tr.mu.Unlock()
		}
		return
	}

	_, _ = tr.client.EditMessage(tr.chatID, msgID, text, opts)
}

func (tr *ThrottledReporter) formatUpdate(u ProgressUpdate) string {
	action := u.Action
	if action == "" {
		action = "Processing"
	}

	bar := DrawProgressBar(u.Percentage, 10)
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("⚡ <b>%s Task #%d</b> <code>[%s]</code>\n\n", html.EscapeString(action), tr.taskID, tr.taskType))

	if u.FileName != "" {
		sb.WriteString(fmt.Sprintf("📄 <b>File:</b> <code>%s</code>\n", html.EscapeString(u.FileName)))
	}

	sb.WriteString(fmt.Sprintf("📊 <b>Progress:</b> <code>%s</code> <code>%.1f%%</code>\n", bar, u.Percentage))

	if u.Total > 0 {
		sb.WriteString(fmt.Sprintf("📦 <b>Size:</b> <code>%s / %s</code>\n", FormatBytes(u.Current), FormatBytes(u.Total)))
	} else if u.Current > 0 {
		sb.WriteString(fmt.Sprintf("📦 <b>Downloaded:</b> <code>%s</code>\n", FormatBytes(u.Current)))
	}

	if u.Speed > 0 {
		sb.WriteString(fmt.Sprintf("🚀 <b>Speed:</b> <code>%s</code>", FormatSpeed(u.Speed)))
	}
	if u.ETA > 0 {
		sb.WriteString(fmt.Sprintf(" | ⏱️ <b>ETA:</b> <code>%s</code>", FormatDuration(u.ETA)))
	}
	sb.WriteString("\n")

	if u.RawStatus != "" {
		sb.WriteString(fmt.Sprintf("ℹ️ <code>%s</code>\n", html.EscapeString(u.RawStatus)))
	}

	sb.WriteString(fmt.Sprintf("\n<i>Use /cancel %d to cancel</i>", tr.taskID))

	return sb.String()
}
