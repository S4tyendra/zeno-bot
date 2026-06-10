package aichat

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/amarnathcjd/gogram/telegram"
	"github.com/google/uuid"
	"google.golang.org/genai"
)

var gptTools = []map[string]interface{}{
	{
		"type":        "function",
		"name":        "create_image",
		"description": "Generate an image from a text prompt. Returns the file path of the generated image.",
		"parameters": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"prompt":       map[string]interface{}{"type": "string", "description": "Detailed prompt describing the image"},
				"aspect_ratio": map[string]interface{}{"type": "string", "description": "Values: 1:1, 9:16, 16:9, 3:4, 4:3, 3:2, 2:3, 5:4, 4:5, 21:9"},
				"high_quality": map[string]interface{}{"type": "boolean"},
			},
			"required": []string{"prompt"},
		},
	},
	{
		"type":        "function",
		"name":        "send_file",
		"description": "Send a file to the user in the chat.",
		"parameters": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"file_path": map[string]interface{}{"type": "string"},
			},
			"required": []string{"file_path"},
		},
	},
	{
		"type":        "function",
		"name":        "run_code",
		"description": "Execute code in a sandboxed container. Available: python, bash, javascript",
		"parameters": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"language": map[string]interface{}{"type": "string", "enum": []string{"python", "bash", "javascript"}},
				"code":     map[string]interface{}{"type": "string"},
			},
			"required": []string{"language", "code"},
		},
	},
	{
		"type": "function",
		"name": "get_latest_data",
		"description": "Search the web for real-time information. Returns sources and details.",
		"parameters": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query": map[string]interface{}{"type": "string"},
			},
			"required": []string{"query"},
		},
	},
}


func handleGPT(m *telegram.NewMessage) error {
	if !FilterAllowed(m) {
		return nil
	}
	return processGPTRequest(m, m.Args())
}

func processGPTRequest(m *telegram.NewMessage, query string) error {
	chatID := m.ChatID()
	replyToMsgID := m.ReplyToMsgID()

	historyLimit := 20
	if m.IsPrivate() {
		historyLimit = 30
	}

	chatHistory := fetchChatHistoryExcluding(chatID, m.ID, replyToMsgID, historyLimit)
	var contextBuilder strings.Builder

	// Enforce SYSTEM_PROMPT directly in the context block since the unofficial API
	// can sometimes ignore the `instructions` wrapper field.
	contextBuilder.WriteString("[CORE SYSTEM INSTRUCTIONS]\n")
	contextBuilder.WriteString(SYSTEM_PROMPT)
	contextBuilder.WriteString("\n[END INSTRUCTIONS]\n\n")

	if len(chatHistory) > 0 {
		for _, msg := range chatHistory {
			contextBuilder.WriteString(msg.Sender)
			contextBuilder.WriteString(": ")
			contextBuilder.WriteString(strings.ReplaceAll(msg.Text, "\n", "\\n"))
			contextBuilder.WriteString("\n")
		}
		contextBuilder.WriteString("----\n")
	}

	senderName := getSenderName(m)
	if query != "" {
		contextBuilder.WriteString(senderName)
		contextBuilder.WriteString(": ")
		contextBuilder.WriteString(strings.ReplaceAll(query, "\n", "\\n"))
		contextBuilder.WriteString("\n")
	}

	// image parts collected from current message and replied-to message
	var imageParts []map[string]interface{}

	// ── media from the current message ───────────────────────────────────
	if m.Media() != nil {
		mediaData, mimeType, fileName := downloadMedia(m)
		if mediaData != nil {
			log.Printf("[AiChat GPT] Received media: %s (%s)", fileName, mimeType)
			if strings.HasPrefix(mimeType, "image/") {
				encoded := base64.StdEncoding.EncodeToString(mediaData)
				imageParts = append(imageParts, map[string]interface{}{
					"type":      "input_image",
					"image_url": fmt.Sprintf("data:%s;base64,%s", mimeType, encoded),
				})
				contextBuilder.WriteString(fmt.Sprintf("[User sent an image: %s]\n", fileName))
			} else if mimeType == "application/pdf" || strings.HasSuffix(strings.ToLower(fileName), ".pdf") {
				// Send PDF directly — API extracts text + renders pages internally
				encoded := base64.StdEncoding.EncodeToString(mediaData)
				imageParts = append(imageParts, map[string]interface{}{
					"type":      "input_file",
					"filename":  fileName,
					"file_data": fmt.Sprintf("data:application/pdf;base64,%s", encoded),
				})
				contextBuilder.WriteString(fmt.Sprintf("[User sent PDF: %s]\n", fileName))
			} else if isTextFile(fileName, mimeType) {
				contextBuilder.WriteString(fmt.Sprintf("\n--- File: %s ---\n%s\n---\n", fileName, string(mediaData)))
			} else {
				contextBuilder.WriteString(fmt.Sprintf("[User sent unsupported file: %s]\n", fileName))
			}
		}
	}

	// ── replied-to message context + media ────────────────────────────────
	if replyToMsgID != 0 {
		replyMsg, replyMediaParts := getMessageWithMedia(chatID, replyToMsgID)
		if replyMsg != nil {
			contextBuilder.WriteString("---\n")
			contextBuilder.WriteString(replyMsg.Sender)
			contextBuilder.WriteString(": ")
			contextBuilder.WriteString(strings.ReplaceAll(replyMsg.Text, "\n", "\\n"))
			contextBuilder.WriteString("\n---\nYou are replying to the triggered message user.\n")

			for _, rp := range replyMediaParts {
				if rp.InlineData == nil {
					continue
				}
				encoded := base64.StdEncoding.EncodeToString(rp.InlineData.Data)
				if rp.InlineData.MIMEType == "application/pdf" {
					imageParts = append(imageParts, map[string]interface{}{
						"type":      "input_file",
						"filename":  "reply.pdf",
						"file_data": fmt.Sprintf("data:application/pdf;base64,%s", encoded),
					})
				} else {
					imageParts = append(imageParts, map[string]interface{}{
						"type":      "input_image",
						"image_url": fmt.Sprintf("data:%s;base64,%s", rp.InlineData.MIMEType, encoded),
					})
				}
			}
		}
	}

	if query == "" && replyToMsgID == 0 && len(chatHistory) == 0 && m.Media() == nil {
		m.Reply("Usage: /gpt <query> or reply to a message with /gpt")
		return nil
	}

	placeholder, err := m.Reply("...")
	if err != nil {
		log.Printf("[AiChat GPT] Failed to send placeholder: %v", err)
		return nil
	}

	// Build content: text part first, then image parts
	contentParts := []map[string]interface{}{
		{"type": "input_text", "text": contextBuilder.String()},
	}
	contentParts = append(contentParts, imageParts...)

	messages := []map[string]interface{}{
		{"role": "user", "content": contentParts},
	}

	responseText, sourcesURL, err := chatGPTLoop(messages, placeholder, m)
	if err != nil {
		log.Printf("[AiChat GPT] Error: %v", err)
		placeholder.Edit("Something went wrong. Try again later.")
		return nil
	}

	if sourcesURL != "" {
		responseText += fmt.Sprintf("\n\n[SOURCES](%s)", sourcesURL)
	}

	return sendLargeResponse(m, placeholder, responseText)
}

func isTextFile(fileName, mime string) bool {
	if strings.HasPrefix(mime, "text/") {
		return true
	}
	ext := strings.ToLower(filepath.Ext(fileName))
	textExts := map[string]bool{".txt": true, ".go": true, ".py": true, ".js": true, ".c": true, ".cpp": true, ".h": true, ".html": true, ".css": true, ".md": true, ".json": true, ".yaml": true, ".yml": true, ".csv": true, ".sh": true, ".bat": true}
	return textExts[ext]
}

func pdfToImages(pdfData []byte, maxPages int) ([][]byte, error) {
	tmpFile, err := ioutil.TempFile("", "*.pdf")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.Write(pdfData)
	tmpFile.Close()

	tmpDir, err := ioutil.TempDir("", "pdf_images")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)

	cmd := exec.Command("pdftoppm", "-jpeg", "-f", "1", "-l", fmt.Sprintf("%d", maxPages), tmpFile.Name(), filepath.Join(tmpDir, "page"))
	if err := cmd.Run(); err != nil {
		return nil, err
	}

	files, err := filepath.Glob(filepath.Join(tmpDir, "page-*.jpg"))
	if err != nil {
		return nil, err
	}

	var result [][]byte
	for _, f := range files {
		data, err := ioutil.ReadFile(f)
		if err == nil {
			result = append(result, data)
		}
	}
	return result, nil
}


type pendingToolCall struct {
	CallID    string
	Name      string
	Arguments string
}

func chatGPTLoop(messages []map[string]interface{}, placeholder *telegram.NewMessage, m *telegram.NewMessage) (string, string, error) {
	var combinedText string
	var sourcesURL string
	for {
		text, pendings, err := streamGPTOnce(messages, placeholder)
		if err != nil {
			return combinedText, sourcesURL, err
		}

		if text != "" {
			combinedText += text
		}

		if len(pendings) == 0 {
			break
		}

		for _, tc := range pendings {
			title := fmt.Sprintf("🔧 Calling %s...", tc.Name)
			placeholder.Edit(title)

			var args map[string]any
			json.Unmarshal([]byte(tc.Arguments), &args)

			fc := &genai.FunctionCall{
				Name: tc.Name,
				Args: args,
			}
			res := executeFunctionCall(fc, m.ChatID(), m.ReplyToMsgID())

			// Mirror Gemini: extract & strip sources_url from get_latest_data
			if tc.Name == "get_latest_data" {
				if u, ok := res["sources_url"].(string); ok {
					sourcesURL = u
				}
				delete(res, "sources_url")
			}

			resStr, _ := json.Marshal(res)
			log.Printf("[AiChat GPT] %s → %s", tc.Name, truncateString(string(resStr), 300))

			messages = append(messages, map[string]interface{}{
				"type":      "function_call",
				"call_id":   tc.CallID,
				"name":      tc.Name,
				"arguments": tc.Arguments,
			})
			messages = append(messages, map[string]interface{}{
				"type":    "function_call_output",
				"call_id": tc.CallID,
				"output":  string(resStr),
			})
		}
	}
	return combinedText, sourcesURL, nil
}

func streamGPTOnce(messages []map[string]interface{}, placeholder *telegram.NewMessage) (string, []pendingToolCall, error) {
	auth, err := GetGPTAuth()
	if err != nil {
		return "", nil, fmt.Errorf("auth error: %v", err)
	}

	accessToken := auth.AccessToken
	accountID := auth.AccountID

	body := map[string]interface{}{
		"model":        "gpt-5.4",
		"input":        messages,
		"stream":       true,
		"store":        false,
		"reasoning":    map[string]string{"effort": "medium"},
		"instructions": SYSTEM_PROMPT,
		"tools":        gptTools,
	}

	reqBody, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", "https://chatgpt.com/backend-api/codex/responses", strings.NewReader(string(reqBody)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("OpenAI-Beta", "responses=experimental")
	req.Header.Set("originator", "codex_cli_rs")
	uid := pseudoUUID()
	req.Header.Set("session_id", uid)
	req.Header.Set("chatgpt-account-id", accountID)

	client := &http.Client{Timeout: 90 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		b, _ := ioutil.ReadAll(resp.Body)
		return "", nil, fmt.Errorf("status %d: %s", resp.StatusCode, string(b))
	}

	scanner := bufio.NewScanner(resp.Body)
	// Large SSE lines (e.g. base64 images in tool results) can exceed default 64KB
	scanner.Buffer(make([]byte, 20*1024*1024), 20*1024*1024)
	var fullText string
	lastEdit := time.Now()

	toolCalls := make(map[string]*pendingToolCall)
	itemToCall := make(map[string]string)

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := line[6:]
		if payload == "[DONE]" {
			break
		}

		var ev map[string]interface{}
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			continue
		}

		typ, _ := ev["type"].(string)

		if typ == "response.output_text.delta" {
			if delta, ok := ev["delta"].(string); ok {
				fullText += delta
				if time.Since(lastEdit) > 1500*time.Millisecond && len(fullText) > 0 {
					placeholder.Edit(fullText + " ✍️")
					lastEdit = time.Now()
				}
			}
		} else if typ == "response.output_item.added" {
			item, ok := ev["item"].(map[string]interface{})
			if ok && item["type"] == "function_call" {
				cid, _ := item["call_id"].(string)
				iid, ok := item["id"].(string)
				if !ok {
					iid = cid
				}
				name, _ := item["name"].(string)

				if cid != "" && toolCalls[cid] == nil {
					toolCalls[cid] = &pendingToolCall{CallID: cid, Name: name}
					itemToCall[iid] = cid
					itemToCall[cid] = cid
				}
			}
		} else if typ == "response.function_call_arguments.delta" {
			rawID, _ := ev["call_id"].(string)
			if rawID == "" {
				rawID, _ = ev["item_id"].(string)
			}
			if rawID == "" {
				rawID, _ = ev["id"].(string)
			}
			cid := itemToCall[rawID]
			delta, _ := ev["delta"].(string)
			if tc, ok := toolCalls[cid]; ok && tc != nil {
				tc.Arguments += delta
			}
		} else if typ == "response.function_call_arguments.done" {
			rawID, _ := ev["call_id"].(string)
			if rawID == "" {
				rawID, _ = ev["item_id"].(string)
			}
			if rawID == "" {
				rawID, _ = ev["id"].(string)
			}
			cid := itemToCall[rawID]
			if tc, ok := toolCalls[cid]; ok && tc != nil {
				final, _ := ev["arguments"].(string)
				if final != "" {
					tc.Arguments = final
				}
			}
		}
	}

	var pending []pendingToolCall
	for _, tc := range toolCalls {
		pending = append(pending, *tc)
	}

	return fullText, pending, nil
}

func pseudoUUID() string {
	return uuid.New().String()
}
