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

	case <-time.After(3 * time.Second):
		go func() {
			err := <-done
			status := "done"
			if err != nil {
				status = "error"
			}
			db.Pool.Exec(context.Background(), `UPDATE tasks SET status = $1, finished_at = NOW() WHERE task_id = $2`, status, taskID)

			tailOut, _ := exec.Command("tail", "-n", "20", logPath).Output()
			var msg string
			if status == "error" {
				msg = fmt.Sprintf("❌ **Task Failed** (`%s`)\n```\n%s\n```", taskID, string(tailOut))
			} else {
				msg = fmt.Sprintf("✅ **Task Finished** (`%s`)\n```\n%s\n```", taskID, string(tailOut))
			}

			botClient.SendMessage(chatID, msg, &telegram.SendOptions{
				ParseMode: "Markdown",
				ReplyTo: &telegram.InputReplyToMessage{
					ReplyToMsgID: msgID,
				},
			})
		}()

		return map[string]any{
			"task_id":  taskID,
			"status":   "running",
			"log_path": logPath,
			"message":  fmt.Sprintf("⚙️ Task running in background. Log: %s. You'll be pinged when it's done.", logPath),
		}
	}
}
