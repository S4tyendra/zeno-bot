package aichat

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/amarnathcjd/gogram/telegram"
	"google.golang.org/genai"

	"zeno/config"
)

type usageAcc struct {
	Prompt     int32
	Cached     int32
	Candidates int32
	Thoughts   int32
	ToolPrompt int32
	Total      int32
	Calls      int
	Model      string
	Version    string
	Thinking   string
	Stream     bool
	Elapsed    time.Duration
	ImageCalls int
}

func (u *usageAcc) add(m *genai.GenerateContentResponseUsageMetadata) {
	if u == nil || m == nil {
		return
	}
	u.Prompt += m.PromptTokenCount
	u.Cached += m.CachedContentTokenCount
	u.Candidates += m.CandidatesTokenCount
	u.Thoughts += m.ThoughtsTokenCount
	u.ToolPrompt += m.ToolUsePromptTokenCount
	u.Total += m.TotalTokenCount
	u.Calls++
}

type modelRates struct {
	In    float64
	Out   float64
	Cache float64
}

func ratesForModel(model string) modelRates {
	m := strings.ToLower(model)
	switch {
	case strings.Contains(m, "3.8-flash"), strings.Contains(m, "3.7-flash"), strings.Contains(m, "3.6-flash"):
		return modelRates{In: 0.75, Out: 3.75, Cache: 0.075}
	case strings.Contains(m, "3.5-flash-lite"):
		return modelRates{In: 0.30, Out: 2.50, Cache: 0.03}
	case strings.Contains(m, "3.5-flash"):
		return modelRates{In: 1.50, Out: 9.00, Cache: 0.15}
	case strings.Contains(m, "3.1-pro"), strings.Contains(m, "3-pro"):
		return modelRates{In: 2.00, Out: 12.00, Cache: 0.20}
	case strings.Contains(m, "3.1-flash-lite"):
		return modelRates{In: 0.25, Out: 1.50, Cache: 0.025}
	case strings.Contains(m, "3-flash"):
		return modelRates{In: 0.50, Out: 3.00, Cache: 0.05}
	case strings.Contains(m, "2.5-pro"):
		return modelRates{In: 1.25, Out: 10.00, Cache: 0.125}
	case strings.Contains(m, "2.5-flash-lite"):
		return modelRates{In: 0.10, Out: 0.40, Cache: 0.01}
	case strings.Contains(m, "2.5-flash"):
		return modelRates{In: 0.30, Out: 2.50, Cache: 0.03}
	case strings.Contains(m, "image"):
		return modelRates{In: 0.50, Out: 3.00, Cache: 0.05}
	default:
		return modelRates{In: 1.50, Out: 7.50, Cache: 0.15}
	}
}

func (u usageAcc) costUSD() float64 {
	r := ratesForModel(u.Model)
	uncached := u.Prompt - u.Cached
	if uncached < 0 {
		uncached = 0
	}
	output := u.Candidates + u.Thoughts
	return (float64(uncached)*r.In + float64(u.Cached)*r.Cache + float64(output)*r.Out + float64(u.ToolPrompt)*r.In) / 1_000_000
}

func formatUSD(v float64) string {
	switch {
	case v >= 0.01:
		return fmt.Sprintf("$%.4f", v)
	case v >= 0.0001:
		return fmt.Sprintf("$%.6f", v)
	default:
		return fmt.Sprintf("$%.8f", v)
	}
}

func formatUsageLog(m *telegram.NewMessage, query string, u usageAcc, genErr error) string {
	r := ratesForModel(u.Model)
	uncached := u.Prompt - u.Cached
	if uncached < 0 {
		uncached = 0
	}
	output := u.Candidates + u.Thoughts
	if u.Total == 0 {
		u.Total = u.Prompt + output + u.ToolPrompt
	}

	q := strings.TrimSpace(query)
	if q == "" {
		q = m.Text()
	}
	if len(q) > 160 {
		q = q[:160] + "…"
	}
	if q == "" {
		q = "(media / empty)"
	}

	thinking := u.Thinking
	if thinking == "" {
		thinking = "model default"
	}
	stream := "off"
	if u.Stream {
		stream = "on"
	}

	var b strings.Builder
	b.WriteString("📊 **AI usage**\n")
	fmt.Fprintf(&b, "Model: `%s`\n", u.Model)
	if u.Version != "" && u.Version != u.Model {
		fmt.Fprintf(&b, "Version: `%s`\n", u.Version)
	}
	fmt.Fprintf(&b, "Thinking: `%s` · stream `%s`\n", thinking, stream)
	fmt.Fprintf(&b, "Chat: `%s` · User: %s\n", ensureGroupPrefix(m.ChatID()), getSenderName(m))
	fmt.Fprintf(&b, "Query: %s\n", q)
	fmt.Fprintf(&b, "Turns: `%d`", u.Calls)
	if u.ImageCalls > 0 {
		fmt.Fprintf(&b, " · images `%d`", u.ImageCalls)
	}
	b.WriteByte('\n')
	b.WriteString("\n**Tokens**\n")
	fmt.Fprintf(&b, "• Prompt: `%d`  (uncached `%d` · cached `%d`)\n", u.Prompt, uncached, u.Cached)
	fmt.Fprintf(&b, "• Output: `%d`\n", u.Candidates)
	fmt.Fprintf(&b, "• Thoughts: `%d`\n", u.Thoughts)
	if u.ToolPrompt > 0 {
		fmt.Fprintf(&b, "• Tool prompt: `%d`\n", u.ToolPrompt)
	}
	fmt.Fprintf(&b, "• Total: `%d`\n", u.Total)
	b.WriteString("\n**Cost** (AI only, not GCS)\n")
	fmt.Fprintf(&b, "• **%s**\n", formatUSD(u.costUSD()))
	fmt.Fprintf(&b, "• Rates /1M: in `%s` · cached `%s` · out `%s`\n",
		formatUSD(r.In), formatUSD(r.Cache), formatUSD(r.Out))
	fmt.Fprintf(&b, "\n⏱ `%s`", u.Elapsed.Round(time.Millisecond))
	if genErr != nil {
		fmt.Fprintf(&b, "\n❌ `%s`", truncateString(genErr.Error(), 220))
	}
	return b.String()
}

func postAIUsageLog(m *telegram.NewMessage, query string, u usageAcc, genErr error) {
	if config.LogChannel == 0 || botClient == nil {
		return
	}
	text := formatUsageLog(m, query, u, genErr)
	if _, err := botClient.SendMessage(config.LogChannel, text, &telegram.SendOptions{ParseMode: "Markdown"}); err != nil {
		log.Printf("[AiChat] usage log send failed: %v", err)
		if _, err2 := botClient.SendMessage(config.LogChannel, text, nil); err2 != nil {
			log.Printf("[AiChat] usage log raw send failed: %v", err2)
		}
	}
}
