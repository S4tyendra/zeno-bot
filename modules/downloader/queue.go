package downloader

import (
	"context"
	"fmt"
	"html"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/amarnathcjd/gogram/telegram"
	"zeno/db"
)

var (
	queueWakeupChan  = make(chan struct{}, 10)
	activeTaskCancel sync.Map // map[int64]context.CancelFunc
	botClient        *telegram.Client
	queueOnce        sync.Once
)

func StartQueueWorker(client *telegram.Client) {
	botClient = client
	queueOnce.Do(func() {
		go runQueueLoop()
	})
}

func NotifyQueue() {
	select {
	case queueWakeupChan <- struct{}{}:
	default:
	}
}

func EnqueueTask(ctx context.Context, task *DownloadTask) (int64, error) {
	var taskID int64
	err := db.Pool.QueryRow(ctx, `
		INSERT INTO download_tasks (task_type, chat_id, reply_msg_id, status_msg_id, user_id, url_or_path, custom_name, file_path, file_size, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'queued', NOW(), NOW())
		RETURNING id`,
		task.TaskType, task.ChatID, task.ReplyMsgID, task.StatusMsgID, task.UserID, task.URLOrPath, task.CustomName, task.FilePath, task.FileSize,
	).Scan(&taskID)

	if err != nil {
		return 0, err
	}

	task.ID = taskID
	NotifyQueue()
	return taskID, nil
}

func GetTask(ctx context.Context, taskID int64) (*DownloadTask, error) {
	var t DownloadTask
	err := db.Pool.QueryRow(ctx, `
		SELECT id, task_type, chat_id, reply_msg_id, status_msg_id, user_id, url_or_path, custom_name, file_path, file_size, status, error_msg, created_at, updated_at
		FROM download_tasks WHERE id = $1`, taskID).Scan(
		&t.ID, &t.TaskType, &t.ChatID, &t.ReplyMsgID, &t.StatusMsgID, &t.UserID, &t.URLOrPath, &t.CustomName,
		&t.FilePath, &t.FileSize, &t.Status, &t.ErrorMsg, &t.CreatedAt, &t.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func GetQueuedCount(ctx context.Context) (int, error) {
	var count int
	err := db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM download_tasks WHERE status = 'queued'`).Scan(&count)
	return count, err
}

func GetActiveAndRecentTasks(ctx context.Context) ([]DownloadTask, error) {
	rows, err := db.Pool.Query(ctx, `
		SELECT id, task_type, chat_id, reply_msg_id, status_msg_id, user_id, url_or_path, custom_name, file_path, file_size, status, error_msg, created_at, updated_at
		FROM download_tasks
		WHERE status IN ('queued', 'processing') OR updated_at > NOW() - INTERVAL '2 hours'
		ORDER BY id DESC LIMIT 15`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []DownloadTask
	for rows.Next() {
		var t DownloadTask
		if err := rows.Scan(
			&t.ID, &t.TaskType, &t.ChatID, &t.ReplyMsgID, &t.StatusMsgID, &t.UserID, &t.URLOrPath, &t.CustomName,
			&t.FilePath, &t.FileSize, &t.Status, &t.ErrorMsg, &t.CreatedAt, &t.UpdatedAt,
		); err == nil {
			tasks = append(tasks, t)
		}
	}
	return tasks, nil
}

func CancelTask(ctx context.Context, taskID int64, userID int64, isAdmin bool) error {
	task, err := GetTask(ctx, taskID)
	if err != nil {
		return fmt.Errorf("task #%d not found", taskID)
	}

	if !isAdmin && task.UserID != userID {
		return fmt.Errorf("you do not have permission to cancel task #%d", taskID)
	}

	if task.Status == StatusCompleted || task.Status == StatusFailed || task.Status == StatusCancelled {
		return fmt.Errorf("task #%d is already %s", taskID, task.Status)
	}

	if cancelFunc, ok := activeTaskCancel.Load(taskID); ok {
		if cf, ok := cancelFunc.(context.CancelFunc); ok {
			cf()
		}
		activeTaskCancel.Delete(taskID)
	}

	_, err = db.Pool.Exec(ctx, `
		UPDATE download_tasks SET status = 'cancelled', error_msg = 'Cancelled by user', updated_at = NOW()
		WHERE id = $1 AND status IN ('queued', 'processing')`, taskID)

	_ = os.RemoveAll(taskDir(taskID))

	return err
}

func UpdateTaskStatusMsgID(ctx context.Context, taskID int64, statusMsgID int32) error {
	_, err := db.Pool.Exec(ctx, `UPDATE download_tasks SET status_msg_id = $1 WHERE id = $2`, statusMsgID, taskID)
	return err
}

func UpdateTaskFilePath(ctx context.Context, taskID int64, filePath string, customName string) error {
	fi, _ := os.Stat(filePath)
	var size int64
	if fi != nil {
		size = fi.Size()
	}
	_, err := db.Pool.Exec(ctx, `
		UPDATE download_tasks SET file_path = $1, custom_name = $2, file_size = $3, updated_at = NOW()
		WHERE id = $4`, filePath, customName, size, taskID)
	return err
}

func runQueueLoop() {
	log.Println("[Downloader Queue] Worker started")

	// Reset any orphaned processing tasks from previous bot crash/restart
	resetCtx, resetCancel := context.WithTimeout(context.Background(), 10*time.Second)
	_, _ = db.Pool.Exec(resetCtx, `
		UPDATE download_tasks SET status = 'queued', updated_at = NOW()
		WHERE status = 'processing'`)
	resetCancel()

	for {
		task, err := claimNextTask()
		if err != nil {
			log.Printf("[Downloader Queue] Error claiming task: %v", err)
			time.Sleep(3 * time.Second)
			continue
		}

		if task == nil {
			select {
			case <-queueWakeupChan:
			case <-time.After(5 * time.Second):
			}
			continue
		}

		processTask(task)
	}
}

func claimNextTask() (*DownloadTask, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var t DownloadTask
	err := db.Pool.QueryRow(ctx, `
		WITH next_task AS (
			SELECT id FROM download_tasks
			WHERE status = 'queued'
			ORDER BY id ASC
			LIMIT 1
			FOR UPDATE SKIP LOCKED
		)
		UPDATE download_tasks t
		SET status = 'processing', updated_at = NOW()
		FROM next_task
		WHERE t.id = next_task.id
		RETURNING t.id, t.task_type, t.chat_id, t.reply_msg_id, t.status_msg_id, t.user_id, t.url_or_path, t.custom_name, t.file_path, t.file_size, t.status, t.error_msg, t.created_at, t.updated_at`,
	).Scan(
		&t.ID, &t.TaskType, &t.ChatID, &t.ReplyMsgID, &t.StatusMsgID, &t.UserID, &t.URLOrPath, &t.CustomName,
		&t.FilePath, &t.FileSize, &t.Status, &t.ErrorMsg, &t.CreatedAt, &t.UpdatedAt,
	)

	if err != nil {
		if err == db.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	return &t, nil
}

func processTask(task *DownloadTask) {
	log.Printf("[Downloader Queue] Processing task #%d (%s) for chat %d", task.ID, task.TaskType, task.ChatID)

	taskCtx, taskCancel := context.WithCancel(context.Background())
	activeTaskCancel.Store(task.ID, taskCancel)
	defer func() {
		taskCancel()
		activeTaskCancel.Delete(task.ID)
	}()

	dbCtx, dbCancel := context.WithTimeout(context.Background(), 10*time.Second)
	current, err := GetTask(dbCtx, task.ID)
	dbCancel()
	if err == nil && current != nil && current.Status != StatusProcessing {
		log.Printf("[Downloader Queue] Task #%d skipped (status=%s)", task.ID, current.Status)
		return
	}

	reporter := NewThrottledReporter(botClient, task.ChatID, task.StatusMsgID, task.ID, task.TaskType)

	var (
		filePath string
		fileSize int64
		execErr  error
	)

	switch task.TaskType {
	case TaskTypeTG:
		filePath, fileSize, execErr = DownloadTelegramMedia(taskCtx, botClient, task.ID, task.ChatID, task.ReplyMsgID, task.CustomName, reporter)

	case TaskTypeFast:
		filePath, fileSize, execErr = DownloadAria2(taskCtx, task.ID, task.URLOrPath, task.CustomName, reporter)

	case TaskTypeYTDLP:
		filePath, fileSize, execErr = DownloadYtDlp(taskCtx, task.ID, task.URLOrPath, task.CustomName, isM3U8URL(task.URLOrPath), reporter)

	case TaskTypeURL:
		filePath, fileSize, execErr = DownloadHTTP(taskCtx, task.ID, task.URLOrPath, task.CustomName, reporter)

	case TaskTypeUpload:
		mode := parseUploadMode(task.CustomName)
		execErr = UploadToTelegram(taskCtx, botClient, task, mode, reporter)
		filePath = task.FilePath
		fileSize = task.FileSize

	default:
		execErr = fmt.Errorf("unknown task type: %s", task.TaskType)
	}

	dbCtx, dbCancel = context.WithTimeout(context.Background(), 10*time.Second)
	defer dbCancel()

	if execErr != nil {
		if task.TaskType != TaskTypeUpload {
			_ = os.RemoveAll(taskDir(task.ID))
		}

		if taskCtx.Err() == context.Canceled {
			log.Printf("[Downloader Queue] Task #%d was cancelled", task.ID)
			_, _ = db.Pool.Exec(dbCtx, `
				UPDATE download_tasks SET status = 'cancelled', error_msg = 'Cancelled by user', updated_at = NOW()
				WHERE id = $1 AND status = 'processing'`, task.ID)
			reporter.Finish(fmt.Sprintf("🛑 <b>Task #%d Cancelled</b>", task.ID))
			return
		}

		log.Printf("[Downloader Queue] Task #%d failed: %v", task.ID, execErr)
		_, _ = db.Pool.Exec(dbCtx, `
			UPDATE download_tasks SET status = 'failed', error_msg = $1, updated_at = NOW()
			WHERE id = $2 AND status = 'processing'`, execErr.Error(), task.ID)
		reporter.Finish(fmt.Sprintf("❌ <b>Task #%d Failed</b>\n\n<code>%s</code>", task.ID, html.EscapeString(execErr.Error())))
		return
	}

	log.Printf("[Downloader Queue] Task #%d completed: %s (%d bytes)", task.ID, filePath, fileSize)

	tag, err := db.Pool.Exec(dbCtx, `
		UPDATE download_tasks
		SET status = 'completed', file_path = $1, file_size = $2, updated_at = NOW()
		WHERE id = $3 AND status = 'processing'`, filePath, fileSize, task.ID)
	if err != nil || tag.RowsAffected() == 0 {
		log.Printf("[Downloader Queue] Task #%d completion skipped (already cancelled or missing)", task.ID)
		return
	}

	if task.TaskType != TaskTypeUpload {
		fileName := filepath.Base(filePath)
		markup := telegram.InlineDataGrid(2,
			"📹 Video", fmt.Sprintf("up:video:%d", task.ID),
			"📁 Document", fmt.Sprintf("up:doc:%d", task.ID),
			"🎵 Audio", fmt.Sprintf("up:audio:%d", task.ID),
			"🖼️ Photo", fmt.Sprintf("up:photo:%d", task.ID),
			"⚡ Auto Upload", fmt.Sprintf("up:auto:%d", task.ID),
		)

		reporter.ForceReportWithMarkup(
			fmt.Sprintf("✅ <b>Task #%d Download Completed!</b>\n\n📄 <b>File:</b> <code>%s</code>\n📦 <b>Size:</b> <code>%s</code>\n\nChoose upload format or use <code>/upload %d</code> / <code>/rename %d &lt;new_name&gt;</code>:",
				task.ID, html.EscapeString(fileName), FormatBytes(fileSize), task.ID, task.ID),
			markup,
		)
	}
}
