package aichat

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"google.golang.org/genai"
)

var (
	errHung        = errors.New("google hung (no chunks)")
	retryGaps      = []time.Duration{time.Second, 5 * time.Second, 10 * time.Second, 15 * time.Second, 20 * time.Second}
	hangTimeout    = 45 * time.Second
	maxGenAttempts = 6 // 1 try + 5 retries
)

type streamAcc struct {
	parts        []*genai.Part
	grounding    *genai.GroundingMetadata
	usage        *genai.GenerateContentResponseUsageMetadata
	modelVersion string
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

func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, errHung) {
		return true
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	s := strings.ToLower(err.Error())
	for _, k := range []string{
		"503", "429", "500", "unavailable", "resource exhausted",
		"internal", "reset by peer", "connection reset", "timeout", "temporar",
		"stream terminated", "eof", "hung",
	} {
		if strings.Contains(s, k) {
			return true
		}
	}
	return false
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

	timer := time.NewTimer(hangTimeout)
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

func generateWithRetry(ctx context.Context, model string, contents []*genai.Content, cfg *genai.GenerateContentConfig, status func(string), thoughts *thoughtStreamer) (*streamAcc, error) {
	var last error
	for attempt := 0; attempt < maxGenAttempts; attempt++ {
		if attempt > 0 {
			gap := retryGaps[attempt-1]
			reason := "failed"
			if errors.Is(last, errHung) {
				reason = "hung"
			}
			msg := fmt.Sprintf("⏳ Google %s. Retry %d/5 in %s…", reason, attempt, gap)
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
		acc, err := streamOnce(ctx, model, contents, cfg, thoughts)
		if err == nil {
			return acc, nil
		}
		last = err
		if !isRetryable(err) {
			return nil, err
		}
		log.Printf("[AiChat] generate attempt %d/%d failed: %v", attempt+1, maxGenAttempts, err)
	}
	return nil, fmt.Errorf("google failed after 5 retries: %w", last)
}
