package aichat

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/amarnathcjd/gogram/telegram"
	"github.com/google/uuid"

	"zeno/db"
)

func generateTaskID() string {
	return uuid.New().String()[:8]
}

func RunTaskAsync(language, code string, chatID int64, msgID int32) map[string]any {
	taskID := generateTaskID()
	logPath := filepath.Join("/tmp", fmt.Sprintf("task_%s.log", taskID))

	containerName := os.Getenv("CODE_RUNNER_CONTAINER")
	if containerName == "" {
		containerName = "zeno-code-runner"
	}

	cli := ContainerCLI()
	var cmdArgs []string
	switch language {
	case "python":
		cmdArgs = []string{cli, "exec", containerName, "python3", "-c", code}
	case "bash":
		cmdArgs = []string{cli, "exec", containerName, "sh", "-c", code}
	}

	cmd := exec.Command(cmdArgs[0], cmdArgs[1:]...)

	logFile, err := os.Create(logPath)
	if err != nil {
		return map[string]any{"success": false, "error": fmt.Sprintf("Failed to create log file: %v", err)}
	}

	var fastOutput bytes.Buffer
	multiOut := io.MultiWriter(logFile, &fastOutput)

	cmd.Stdout = multiOut
	cmd.Stderr = multiOut

	_, err = db.Pool.Exec(context.Background(), `
		INSERT INTO tasks (task_id, chat_id, msg_id, command, status, log_path)
		VALUES ($1, $2, $3, $4, 'running', $5)`,
		taskID, chatID, msgID, fmt.Sprintf("%s: %s", language, truncateString(code, 50)), logPath)
	if err != nil {
		log.Printf("[TaskRunner] DB insert failed: %v", err)
	}

	err = cmd.Start()
	if err != nil {
		logFile.Close()
		return map[string]any{"success": false, "error": err.Error()}
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
		logFile.Close()
	}()

	select {
	case err := <-done:
		status := "done"
		if err != nil {
			status = "error"
		}
		db.Pool.Exec(context.Background(), `UPDATE tasks SET status = $1, finished_at = NOW() WHERE task_id = $2`, status, taskID)

		outStr := fastOutput.String()
		if err != nil {
			return map[string]any{
				"success": false,
				"error":   fmt.Sprintf("Execution failed: %v", err),
				"output":  truncateTerminalOutput(outStr),
			}
		}
		return map[string]any{"success": true, "output": truncateTerminalOutput(outStr)}

	case <-time.After(foregroundBudget):
		go func() {
			err := <-done
			status := "done"
			if err != nil {
				status = "error"
			}
			db.Pool.Exec(context.Background(), `UPDATE tasks SET status = $1, finished_at = NOW() WHERE task_id = $2`, status, taskID)

			tailOut, _ := exec.Command("tail", "-n", "60", logPath).Output()
			continueAfterTask(chatID, msgID, taskID, status, err, string(tailOut))
		}()

		return map[string]any{
			"task_id":  taskID,
			"status":   "running",
			"log_path": logPath,
			"message": fmt.Sprintf("⚙️ Task %s is still running in the background (log: %s). "+
				"Tell the user in one short line what you're doing. Do NOT poll it. "+
				"When it finishes you'll automatically get its output in a new turn and can continue the task from there.", taskID, logPath),
		}
	}
}

// foregroundBudget: how long run_code blocks the model loop before the task
// is moved to the background.
const foregroundBudget = 20 * time.Second

// maxContinuations caps auto-continue chains per user message so a model
// that keeps spawning background tasks can't loop forever.
const maxContinuations = 6

var continuations sync.Map // "chat:msg" -> int

// continueAfterTask hands a finished background task's output back to the AI
// as a new turn (new bot message replying to the original request). Falls
// back to posting the raw log when that isn't possible.
func continueAfterTask(chatID int64, msgID int32, taskID, status string, runErr error, tail string) {
	key := fmt.Sprintf("%d:%d", chatID, msgID)
	n := 0
	if v, ok := continuations.Load(key); ok {
		n = v.(int)
	}

	var orig *telegram.NewMessage
	if botClient != nil && n < maxContinuations {
		m, err := botClient.GetMessageByID(chatID, msgID)
		if err != nil {
			log.Printf("[TaskRunner] can't fetch original msg %s for continuation: %v", key, err)
		} else {
			orig = m
		}
	}

	if orig == nil {
		postTaskLog(chatID, msgID, taskID, status, tail)
		return
	}
	continuations.Store(key, n+1)

	errLine := ""
	if runErr != nil {
		errLine = fmt.Sprintf("Exit error: %v\n", runErr)
	}
	note := fmt.Sprintf("<system_event>Background task `%s` (started by your earlier run_code call for this message) finished with status: %s.\n%sLast output lines:\n```\n%s\n```\n"+
		"This is NOT a new user message. Continue the user's original request from where you left off: "+
		"run the next step, send the resulting file, or fix and retry if it failed. "+
		"If the task is fully done, reply to the user with a short result.</system_event>",
		taskID, status, errLine, truncateTerminalOutput(strings.TrimSpace(tail)))

	query := strings.TrimSpace(askPattern.ReplaceAllString(orig.Text(), ""))
	log.Printf("[TaskRunner] continuing AI turn for %s after task %s (%s), depth %d", key, taskID, status, n+1)
	if err := runAITurn(orig, query, note); err != nil {
		log.Printf("[TaskRunner] continuation failed: %v", err)
	}
}

func postTaskLog(chatID int64, msgID int32, taskID, status, tail string) {
	var msg string
	if status == "error" {
		msg = fmt.Sprintf("❌ **Task Failed** (`%s`)\n```\n%s\n```", taskID, tail)
	} else {
		msg = fmt.Sprintf("✅ **Task Finished** (`%s`)\n```\n%s\n```", taskID, tail)
	}
	botClient.SendMessage(chatID, msg, &telegram.SendOptions{
		ParseMode: "Markdown",
		ReplyTo:   &telegram.InputReplyToMessage{ReplyToMsgID: msgID},
	})
}
