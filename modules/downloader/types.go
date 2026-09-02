package downloader

import (
	"time"
)

type TaskType string

const (
	TaskTypeTG     TaskType = "tg"
	TaskTypeURL    TaskType = "url"
	TaskTypeFast   TaskType = "fast"
	TaskTypeYTDLP  TaskType = "ytdlp"
	TaskTypeUpload TaskType = "upload"
)

type TaskStatus string

const (
	StatusQueued     TaskStatus = "queued"
	StatusProcessing TaskStatus = "processing"
	StatusCompleted  TaskStatus = "completed"
	StatusFailed     TaskStatus = "failed"
	StatusCancelled  TaskStatus = "cancelled"
)

type UploadMode string

const (
	UploadAuto     UploadMode = "auto"
	UploadDocument UploadMode = "doc"
	UploadVideo    UploadMode = "video"
	UploadAudio    UploadMode = "audio"
	UploadPhoto    UploadMode = "photo"
)

type DownloadTask struct {
	ID          int64      `json:"id"`
	TaskType    TaskType   `json:"task_type"`
	ChatID      int64      `json:"chat_id"`
	ReplyMsgID  int32      `json:"reply_msg_id"`
	StatusMsgID int32      `json:"status_msg_id"`
	UserID      int64      `json:"user_id"`
	URLOrPath   string     `json:"url_or_path"`
	CustomName  string     `json:"custom_name"`
	FilePath    string     `json:"file_path"`
	FileSize    int64      `json:"file_size"`
	Status      TaskStatus `json:"status"`
	ErrorMsg    string     `json:"error_msg"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

type ProgressUpdate struct {
	Action     string // "Downloading", "Uploading", "Extracting", etc.
	FileName   string
	Current    int64
	Total      int64
	Speed      float64 // bytes/sec
	ETA        float64 // seconds
	Percentage float64
	RawStatus  string // e.g. yt-dlp custom status or aria2 status
}
