package aichat

import (
	"context"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"

	"google.golang.org/genai"
)

var (
	errHung  = errors.New("google hung (no chunks)")
	errEmpty = errors.New("google returned an empty response")
	// gaps between attempts; len(retryGaps)+1 == total attempts
	retryGaps = []time.Duration{2 * time.Second, 4 * time.Second, 6 * time.Second, 10 * time.Second, 15 * time.Second, 20 * time.Second}
	// Long thinking (e.g. crafting an image prompt) can legitimately take a
	// while before the first chunk arrives, so the first-chunk budget is big.
	firstChunkTimeout = 150 * time.Second
	hangTimeout       = 90 * time.Second
	maxGenAttempts    = len(retryGaps) + 1

	permanentErr = regexp.MustCompile(`(?i)\b(400|403|404)\b|invalid_argument|permission_denied|not_found`)
)

type streamAcc struct {
	parts        []*genai.Part
	grounding    *genai.GroundingMetadata
	usage        *genai.GenerateContentResponseUsageMetadata
	modelVersion string
	finishReason genai.FinishReason
	model        string // model that produced this response
}

func (a *streamAcc) add(resp *genai.GenerateContentResponse, thoughts *thoughtStreamer) {
	if resp == nil {
		return
	}
	if resp.UsageMetadata != nil {
		a.usage = resp.UsageMetadata
	}
	if resp.ModelVersion != "" {
		a.modelVersion = resp.ModelVersion
	}
	if len(resp.Candidates) == 0 {
		return
	}
	c := resp.Candidates[0]
	if c.FinishReason != "" {
		a.finishReason = c.FinishReason
	}
	if c.GroundingMetadata != nil {
		a.grounding = c.GroundingMetadata
	}
	if c.Content == nil {
		return
	}
	for _, p := range c.Content.Parts {
		if p == nil {
			continue
		}
		if p.Thought && p.Text != "" && thoughts != nil {
			thoughts.push(p.Text)
		}
		switch {
		case p.FunctionCall != nil:
			a.parts = append(a.parts, &genai.Part{
				FunctionCall:     p.FunctionCall,
				ThoughtSignature: p.ThoughtSignature,
			})
		case p.Text != "":
			if last := a.lastTextPart(p.Thought); last != nil && len(last.ThoughtSignature) == 0 && len(p.ThoughtSignature) == 0 {
				last.Text += p.Text
				break
			}
			a.parts = append(a.parts, &genai.Part{
				Text:             p.Text,
				Thought:          p.Thought,
				ThoughtSignature: p.ThoughtSignature,
			})
		case len(p.ThoughtSignature) > 0:
			a.attachSignature(p.ThoughtSignature)
		}
	}
}

func (a *streamAcc) lastTextPart(thought bool) *genai.Part {
	if n := len(a.parts); n > 0 {
		last := a.parts[n-1]
		if last.FunctionCall == nil && last.FunctionResponse == nil && last.FileData == nil && last.InlineData == nil && last.Thought == thought {
			return last
		}
	}
	return nil
}

func (a *streamAcc) attachSignature(sig []byte) {
	for _, p := range a.parts {
		if p.FunctionCall != nil && len(p.ThoughtSignature) == 0 {
			p.ThoughtSignature = sig
			return
		}
	}
	for i := len(a.parts) - 1; i >= 0; i-- {
		if len(a.parts[i].ThoughtSignature) == 0 {
			a.parts[i].ThoughtSignature = sig
			return
		}
	}
	a.parts = append(a.parts, &genai.Part{ThoughtSignature: sig})
}

func (a *streamAcc) modelContent() *genai.Content {
	if a == nil || len(a.parts) == 0 {
		return nil
	}
	return &genai.Content{Role: genai.RoleModel, Parts: a.parts}
}

func (a *streamAcc) answerText() string {
	if a == nil {
		return ""
	}
	var b strings.Builder
	for _, p := range a.parts {
		if !p.Thought && p.Text != "" {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

// empty reports whether the model produced nothing usable (no visible text
// and no function call) — thoughts alone don't count.
func (a *streamAcc) empty() bool {
	return a == nil || (strings.TrimSpace(a.answerText()) == "" && len(a.functionCallParts()) == 0)
}

func (a *streamAcc) functionCallParts() []*genai.Part {
	if a == nil {
		return nil
	}
	var out []*genai.Part
	for _, p := range a.parts {
		if p.FunctionCall != nil {
			out = append(out, p)
		}
	}
	return out
}

func (a *streamAcc) ensureFunctionCallSignatures() {
	if a == nil {
		return
	}
	var lastSig []byte
	firstFC := -1
	for i, p := range a.parts {
		if len(p.ThoughtSignature) > 0 {
			lastSig = p.ThoughtSignature
		}
		if p.FunctionCall != nil && firstFC < 0 {
			firstFC = i
		}
	}
	if firstFC >= 0 && len(a.parts[firstFC].ThoughtSignature) == 0 && len(lastSig) > 0 {
		a.parts[firstFC].ThoughtSignature = lastSig
	}
}

// isRetryable: anything except our own cancellation/deadline is worth another
// shot — Vertex throws a zoo of transient errors that don't match any list.
func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	return true
}

// isPermanent: errors where retrying the same model is pointless (bad request,
// model not found, no access). We jump straight to the lite model instead.
func isPermanent(err error) bool {
	if err == nil || errors.Is(err, errHung) || errors.Is(err, errEmpty) {
		return false
	}
	return permanentErr.MatchString(err.Error())
}

func streamOnce(ctx context.Context, model string, contents []*genai.Content, cfg *genai.GenerateContentConfig, thoughts *thoughtStreamer) (*streamAcc, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	type ev struct {
		resp *genai.GenerateContentResponse
		err  error
	}
	ch := make(chan ev)
	go func() {
		defer close(ch)
		for resp, err := range genaiClient.Models.GenerateContentStream(ctx, model, contents, cfg) {
			select {
			case ch <- ev{resp: resp, err: err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()

	timer := time.NewTimer(firstChunkTimeout)
	defer timer.Stop()

	var acc streamAcc
	got := false
	for {
		select {
		case e, ok := <-ch:
			if !ok {
				if !got {
					return nil, fmt.Errorf("empty stream from google")
				}
				if thoughts != nil {
					thoughts.flush()
				}
				acc.ensureFunctionCallSignatures()
				return &acc, nil
			}
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(hangTimeout)
			if e.err != nil {
				return nil, e.err
			}
			got = true
			acc.add(e.resp, thoughts)
		case <-timer.C:
			cancel()
			return nil, errHung
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// genPlan drives one request's generations: retries with backoff, nudges the
// model after empty replies, and after liteAfterFails failures on the default
// model switches (stickily, for the rest of the request) to the lite model.
type genPlan struct {
	primary    string
	primaryCfg *genai.GenerateContentConfig
	lite       string
	liteCfg    *genai.GenerateContentConfig

	onLite       bool
	primaryFails int
}

func (g *genPlan) current() (string, *genai.GenerateContentConfig) {
	if g.onLite {
		return g.lite, g.liteCfg
	}
	return g.primary, g.primaryCfg
}

func (g *genPlan) canFallback() bool {
	return !g.onLite && g.lite != "" && g.lite != g.primary && g.liteCfg != nil
}

func (g *genPlan) switchToLite(status func(string)) {
	g.onLite = true
	log.Printf("[AiChat] falling back %s -> %s", g.primary, g.lite)
	if status != nil {
		status(fmt.Sprintf("🪶 `%s` keeps failing, switching to `%s`…", g.primary, g.lite))
	}
}

var emptyNudge = &genai.Content{
	Role: genai.RoleUser,
	Parts: []*genai.Part{{Text: "<system_note>Your previous reply was empty. Do not end with nothing: " +
		"either call the tool you need next, or write your reply to the user now (short is fine).</system_note>"}},
}

// generate runs one model step. allowEmpty accepts an empty reply as a valid
// "done" (e.g. right after the model already delivered a file).
func (g *genPlan) generate(ctx context.Context, contents []*genai.Content, status func(string), thoughts *thoughtStreamer, allowEmpty bool) (*streamAcc, error) {
	var last error
	for attempt := 0; attempt < maxGenAttempts; attempt++ {
		if attempt > 0 {
			gap := retryGaps[attempt-1]
			reason := "failed"
			switch {
			case errors.Is(last, errHung):
				reason = "stalled"
			case errors.Is(last, errEmpty):
				reason = "replied empty"
			}
			model, _ := g.current()
			msg := fmt.Sprintf("⏳ Google %s. Retry %d/%d in %s (`%s`)…", reason, attempt, maxGenAttempts-1, gap, model)
			log.Printf("[AiChat] %s (%v)", msg, last)
			if status != nil {
				status(msg)
			}
			select {
			case <-time.After(gap):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		if thoughts != nil {
			thoughts.reset()
		}

		model, cfg := g.current()
		req := contents
		if errors.Is(last, errEmpty) {
			req = append(append([]*genai.Content{}, contents...), emptyNudge)
		}

		acc, err := streamOnce(ctx, model, req, cfg, thoughts)
		if err == nil {
			acc.model = model
			if !acc.empty() || allowEmpty {
				return acc, nil
			}
			log.Printf("[AiChat] empty reply from %s finish=%s parts=%d", model, acc.finishReason, len(acc.parts))
			err = errEmpty
		}
		last = err
		if !isRetryable(err) {
			return nil, err
		}
		log.Printf("[AiChat] generate attempt %d/%d on %s failed: %v", attempt+1, maxGenAttempts, model, err)

		if !g.onLite {
			g.primaryFails++
			if g.canFallback() && (g.primaryFails >= liteAfterFails || isPermanent(err)) {
				g.switchToLite(status)
				continue
			}
		}
		if isPermanent(err) && !g.canFallback() {
			// Same bad request on the only model left; retrying won't fix it.
			return nil, err
		}
	}
	return nil, fmt.Errorf("google failed after %d attempts: %w", maxGenAttempts, last)
}
