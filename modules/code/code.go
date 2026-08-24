package code

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/amarnathcjd/gogram/telegram"

	"zeno/modules/aichat"
)

var langMap = map[string]string{
	"py":         "python",
	"python":     "python",
	"sh":         "bash",
	"bash":       "bash",
}

func Register(client *telegram.Client) {
	client.On("cmd:code", handleCode)
}

func handleCode(m *telegram.NewMessage) error {
	if !aichat.FilterAllowed(m) {
		return nil
	}

	args := m.Args()
	replyToMsgID := m.ReplyToMsgID()

	var langWord string
	var codeText string

	if replyToMsgID != 0 {
		// Fetch replied-to message
		msgs, err := m.Client.GetMessages(m.ChatID(), &telegram.SearchOption{IDs: []int32{replyToMsgID}})
		if err != nil || len(msgs) == 0 {
			m.Reply("❌ Couldn't fetch replied-to message.")
			return nil
		}
		target := msgs[0]
		codeText = target.Text()
		if codeText == "" {
			m.Reply("❌ Replied-to message contains no text/code.")
			return nil
		}
		langWord = strings.TrimSpace(args)
	} else {
		args = strings.TrimSpace(args)
		if args == "" {
			sendUsage(m)
			return nil
		}

		// Parse language and code from arguments
		idx := strings.IndexFunc(args, func(r rune) bool {
			return r == ' ' || r == '\n' || r == '\t'
		})
		if idx == -1 {
			sendUsage(m)
			return nil
		}

		langWord = strings.TrimSpace(args[:idx])
		codeText = strings.TrimSpace(args[idx:])
	}

	lang := langMap[strings.ToLower(langWord)]
	if lang == "" {
		m.Reply("❌ Invalid language. Supported: `py`/`python`, `sh`/`bash`", &telegram.SendOptions{ParseMode: "Markdown"})
		return nil
	}

	codeText = cleanCode(codeText)
	if codeText == "" {
		m.Reply("❌ Code block is empty.")
		return nil
	}

	containerName := os.Getenv("CODE_RUNNER_CONTAINER")
	if containerName == "" {
		containerName = "zeno-code-runner"
	}

	cli := aichat.ContainerCLI()
	var cmdArgs []string
	switch lang {
	case "python":
		cmdArgs = []string{cli, "exec", containerName, "python3", "-c", codeText}
	case "bash":
		cmdArgs = []string{cli, "exec", containerName, "bash", "-c", codeText}
	}

	log.Printf("[CodeRunner] Running manual code (%s) for user %d", lang, m.SenderID())

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, cmdArgs[0], cmdArgs[1:]...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	output := stdout.String()
	errOutput := stderr.String()

	if ctx.Err() == context.DeadlineExceeded {
		m.Reply("❌ **Timeout:** Execution exceeded 2 minutes limit.", &telegram.SendOptions{ParseMode: "Markdown"})
		return nil
	}

	if err != nil {
		var msgText string
		if errOutput != "" {
			msgText = fmt.Sprintf("❌ **Execution failed:**\n```\n%s\n```", errOutput)
		} else {
			msgText = fmt.Sprintf("❌ **Execution failed:** `%v`", err)
		}
		if output != "" {
			msgText += fmt.Sprintf("\n\n💻 **Output:**\n```\n%s\n```", output)
		}
		return sendResponse(m, msgText)
	}

	var responseText string
	if output != "" {
		responseText = fmt.Sprintf("💻 **Output:**\n```\n%s\n```", output)
	} else {
		responseText = "💻 Execution successful (no output)."
	}

	return sendResponse(m, responseText)
}

func cleanCode(code string) string {
	code = strings.TrimSpace(code)
	if strings.HasPrefix(code, "```") {
		if idx := strings.Index(code, "\n"); idx != -1 {
			code = code[idx+1:]
		} else {
			code = strings.TrimPrefix(code, "```")
		}
		code = strings.TrimSuffix(code, "```")
		code = strings.TrimSpace(code)
	}
	return code
}

func sendResponse(m *telegram.NewMessage, text string) error {
	if len(text) > 4000 {
		url, err := aichat.UploadToTelegraph("Code Execution", text)
		if err == nil {
		_, err := m.Reply(fmt.Sprintf("📋 **Output too long:** [View Full Result](%s)", url), &telegram.SendOptions{ParseMode: "Markdown"})
		return err
	}
		runes := []rune(text)
		text = string(runes[:3900]) + "\n\n_(Truncated due to length)_"
	}

	_, err := m.Reply(text, &telegram.SendOptions{ParseMode: "Markdown"})
	if err != nil {
		m.Reply(text) // Fallback to raw text without formatting
	}
	return nil
}

func sendUsage(m *telegram.NewMessage) {
	usage := "📖 **Usage:**\n\n" +
		"**/code {py/sh}**\n" +
		"**{code goes here}**\n\n" +
		"_Or reply to a message containing code with:_\n" +
		"**/code {py/sh}**"
	m.Reply(usage, &telegram.SendOptions{ParseMode: "Markdown"})
}
