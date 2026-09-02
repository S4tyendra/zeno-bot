package aichat

import (
	"context"
	"log"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/amarnathcjd/gogram/telegram"
	"google.golang.org/genai"

	"zeno/db"
)

const thinkingSettingKey = "thinking"

type ThinkingPrefs struct {
	Level  string `json:"level"`
	Stream bool   `json:"stream"`
}

var (
	thinkingMu    sync.Mutex
	thinkingCache ThinkingPrefs
	thinkingExp   time.Time
)

var headingLine = regexp.MustCompile(`^(?:#{1,3}\s+|\*\*)(.+?)(?:\*\*)?\s*$`)

func LoadThinkingPrefs() ThinkingPrefs {
	thinkingMu.Lock()
	defer thinkingMu.Unlock()
	if time.Now().Before(thinkingExp) {
		return thinkingCache
	}
	var prefs ThinkingPrefs
	if err := db.GetSetting(context.Background(), thinkingSettingKey, &prefs); err != nil {
		thinkingCache = ThinkingPrefs{}
	} else {
		thinkingCache = prefs
	}
	thinkingExp = time.Now().Add(15 * time.Second)
	return thinkingCache
}

func SaveThinkingLevel(level string) error {
	level = normalizeLevelName(level)
	if level == "" {
		return errInvalidThinking
	}
	prefs := LoadThinkingPrefs()
	prefs.Level = level
	if err := db.SetSetting(context.Background(), thinkingSettingKey, prefs); err != nil {
		return err
	}
	thinkingMu.Lock()
	thinkingCache = prefs
	thinkingExp = time.Now().Add(15 * time.Second)
	thinkingMu.Unlock()
	return nil
}

func SaveThinkStream(on bool) error {
	prefs := LoadThinkingPrefs()
	prefs.Stream = on
	if err := db.SetSetting(context.Background(), thinkingSettingKey, prefs); err != nil {
		return err
	}
	thinkingMu.Lock()
	thinkingCache = prefs
	thinkingExp = time.Now().Add(15 * time.Second)
	thinkingMu.Unlock()
	return nil
}

var errInvalidThinking = errString("invalid thinking level (use minimal, low, medium, high, max, off)")

type errString string

func (e errString) Error() string { return string(e) }

func normalizeLevelName(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "minimal", "min":
		return "minimal"
	case "low":
		return "low"
	case "medium", "med":
		return "medium"
	case "high":
		return "high"
	case "max", "maximum":
		return "max"
	case "off", "disable", "none":
		return "off"
	default:
		return ""
	}
}

func thinkingFamily(model string) string {
	m := strings.ToLower(model)
	switch {
	case strings.Contains(m, "flash-lite"):
		return "lite"
	case strings.Contains(m, "3.8-flash"), strings.Contains(m, "3.7-flash"):
		return "flash37"
	case strings.Contains(m, "3-pro-preview") && !strings.Contains(m, "3.1"):
		return "pro-preview"
	case strings.Contains(m, "3.1-pro"), strings.Contains(m, "3-pro"):
		return "pro"
	case strings.Contains(m, "3.6-flash"), strings.Contains(m, "3.5-flash"), strings.Contains(m, "gemini-3"):
		return "flash35"
	default:
		return "other"
	}
}

func EffectiveThinkingLevel(model, requested string) string {
	lvl := clampThinking(model, requested)
	if lvl == "" {
		if requested == "" {
			return "model default"
		}
		return "omitted"
	}
	return strings.ToLower(string(lvl))
}

func clampThinking(model, requested string) genai.ThinkingLevel {
	req := normalizeLevelName(requested)
	if req == "max" {
		req = "high"
	}
	family := thinkingFamily(model)

	allowed := map[string]bool{"minimal": true, "low": true, "medium": true, "high": true}
	switch family {
	case "flash37":
		allowed = map[string]bool{"low": true, "medium": true, "high": true}
	case "pro":
		allowed = map[string]bool{"low": true, "medium": true, "high": true}
	case "pro-preview":
		allowed = map[string]bool{"low": true, "high": true}
	case "other":
		return ""
	}

	if req == "off" {
		if allowed["minimal"] {
			req = "minimal"
		} else {
			req = "low"
		}
	}
	if req != "" && !allowed[req] {
		switch {
		case req == "minimal" && allowed["low"]:
			req = "low"
		case req == "medium" && allowed["high"]:
			req = "high"
		case allowed["medium"]:
			req = "medium"
		case allowed["high"]:
			req = "high"
		case allowed["low"]:
			req = "low"
		}
	}

	switch req {
	case "minimal":
		return genai.ThinkingLevelMinimal
	case "low":
		return genai.ThinkingLevelLow
	case "medium":
		return genai.ThinkingLevelMedium
	case "high":
		return genai.ThinkingLevelHigh
	default:
		return ""
	}
}

func thinkingConfigFor(model string, prefs ThinkingPrefs) *genai.ThinkingConfig {
	isGemini3 := strings.Contains(strings.ToLower(model), "gemini-3")
	if !isGemini3 && !prefs.Stream {
		return nil
	}
	tc := &genai.ThinkingConfig{IncludeThoughts: prefs.Stream}
	if isGemini3 && prefs.Level != "" {
		if lvl := clampThinking(model, prefs.Level); lvl != "" {
			tc.ThinkingLevel = lvl
		}
	}
	if tc.ThinkingLevel == "" && !tc.IncludeThoughts {
		return nil
	}
	return tc
}

func parseThoughtHeading(s string) (heading, body string) {
	lines := strings.Split(s, "\n")
	var bodyLines []string
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if trim == "" {
			if heading != "" {
				bodyLines = append(bodyLines, "")
			}
			continue
		}
		if h, ok := headingFromLine(trim); ok {
			heading = h
			bodyLines = bodyLines[:0]
			continue
		}
		bodyLines = append(bodyLines, line)
	}
	return heading, strings.TrimSpace(strings.Join(bodyLines, "\n"))
}

func headingFromLine(trim string) (string, bool) {
	if !strings.HasPrefix(trim, "#") && !(strings.HasPrefix(trim, "**") && strings.HasSuffix(trim, "**")) {
		return "", false
	}
	m := headingLine.FindStringSubmatch(trim)
	if m == nil {
		return "", false
	}
	h := strings.TrimSpace(strings.Trim(m[1], "*"))
	if h == "" || len(h) > 80 {
		return "", false
	}
	return h, true
}

type thoughtStreamer struct {
	msg     *telegram.NewMessage
	enabled bool
	buf     strings.Builder
	last    time.Time
}

func (t *thoughtStreamer) reset() {
	if t == nil {
		return
	}
	t.buf.Reset()
}

func (t *thoughtStreamer) push(s string) {
	if t == nil || !t.enabled || s == "" || t.msg == nil {
		return
	}
	t.buf.WriteString(s)
	if time.Since(t.last) < 800*time.Millisecond {
		return
	}
	t.flush()
}

func (t *thoughtStreamer) flush() {
	if t == nil || !t.enabled || t.msg == nil {
		return
	}
	heading, body := parseThoughtHeading(t.buf.String())
	text := "💭 thinking..."
	if heading != "" {
		text = "💭 **" + heading + "**"
		if body != "" {
			runes := []rune(body)
			if len(runes) > 400 {
				body = string(runes[len(runes)-400:])
			}
			text += "\n" + body
		}
	} else if body := strings.TrimSpace(t.buf.String()); body != "" {
		runes := []rune(body)
		if len(runes) > 400 {
			body = string(runes[len(runes)-400:])
		}
		text = "💭 " + body
	}
	editStatus(t.msg, text)
	t.last = time.Now()
}

func editStatus(msg *telegram.NewMessage, text string) {
	if msg == nil || text == "" {
		return
	}
	if _, err := msg.Edit(text, &telegram.SendOptions{ParseMode: "Markdown"}); err != nil {
		if _, err2 := msg.Edit(text); err2 != nil {
			log.Printf("[AiChat] status edit failed: %v / %v", err, err2)
		}
	}
}

type progress struct {
	msg      *telegram.NewMessage
	steps    []string
	lastText string
}

func newProgress(msg *telegram.NewMessage) *progress {
	return &progress{msg: msg, lastText: "..."}
}

func (p *progress) step(label string) {
	if p == nil || label == "" {
		return
	}
	if n := len(p.steps); n > 0 && p.steps[n-1] == label {
		return
	}
	p.steps = append(p.steps, label)
	if len(p.steps) > 5 {
		p.steps = p.steps[len(p.steps)-5:]
	}
	p.flush()
}

func (p *progress) flush() {
	if p == nil || p.msg == nil {
		return
	}
	text := "..."
	if len(p.steps) > 0 {
		text = "...\n" + strings.Join(p.steps, " › ")
	}
	if text == p.lastText {
		return
	}
	p.lastText = text
	if _, err := p.msg.Edit(text); err != nil {
		log.Printf("[AiChat] progress edit failed: %v", err)
	}
}
