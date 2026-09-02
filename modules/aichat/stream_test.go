package aichat

import (
	"bytes"
	"testing"

	"google.golang.org/genai"
)

func TestStreamAccKeepsFunctionCallSignature(t *testing.T) {
	sig := []byte("sig-on-fc")
	var acc streamAcc
	acc.add(&genai.GenerateContentResponse{Candidates: []*genai.Candidate{{
		Content: &genai.Content{Parts: []*genai.Part{
			{Text: "**Planning**\n\nneed code", Thought: true},
		}},
	}}}, nil)
	acc.add(&genai.GenerateContentResponse{Candidates: []*genai.Candidate{{
		Content: &genai.Content{Parts: []*genai.Part{
			{Thought: true, ThoughtSignature: []byte("thought-sig")},
		}},
	}}}, nil)
	acc.add(&genai.GenerateContentResponse{Candidates: []*genai.Candidate{{
		Content: &genai.Content{Parts: []*genai.Part{
			{
				FunctionCall:     &genai.FunctionCall{Name: "run_code", Args: map[string]any{"language": "bash"}},
				ThoughtSignature: sig,
			},
		}},
	}}}, nil)

	mcs := acc.modelContent()
	if mcs == nil {
		t.Fatal("empty model content")
	}
	var fcPart *genai.Part
	for _, p := range mcs.Parts {
		if p.FunctionCall != nil {
			fcPart = p
		}
	}
	if fcPart == nil {
		t.Fatal("missing function call part")
	}
	if !bytes.Equal(fcPart.ThoughtSignature, sig) {
		t.Fatalf("function call signature = %q, want %q", fcPart.ThoughtSignature, sig)
	}
	if acc.functionCallParts()[0].FunctionCall.Name != "run_code" {
		t.Fatal("name")
	}
}

func TestStreamAccSignatureOnlyAttachesToFunctionCall(t *testing.T) {
	sig := []byte("late-sig")
	var acc streamAcc
	acc.add(&genai.GenerateContentResponse{Candidates: []*genai.Candidate{{
		Content: &genai.Content{Parts: []*genai.Part{
			{FunctionCall: &genai.FunctionCall{Name: "run_code"}},
		}},
	}}}, nil)
	acc.add(&genai.GenerateContentResponse{Candidates: []*genai.Candidate{{
		Content: &genai.Content{Parts: []*genai.Part{
			{ThoughtSignature: sig},
		}},
	}}}, nil)

	got := acc.functionCallParts()[0].ThoughtSignature
	if !bytes.Equal(got, sig) {
		t.Fatalf("got %q", got)
	}
}

func TestStreamAccCopiesThoughtSigOntoBareFunctionCall(t *testing.T) {
	sig := []byte("thought-then-fc")
	var acc streamAcc
	acc.add(&genai.GenerateContentResponse{Candidates: []*genai.Candidate{{
		Content: &genai.Content{Parts: []*genai.Part{
			{Text: "planning", Thought: true, ThoughtSignature: sig},
		}},
	}}}, nil)
	acc.add(&genai.GenerateContentResponse{Candidates: []*genai.Candidate{{
		Content: &genai.Content{Parts: []*genai.Part{
			{FunctionCall: &genai.FunctionCall{Name: "run_code"}},
		}},
	}}}, nil)
	acc.ensureFunctionCallSignatures()
	got := acc.functionCallParts()[0].ThoughtSignature
	if !bytes.Equal(got, sig) {
		t.Fatalf("got %q", got)
	}
}

func TestStreamAccConcatThoughtText(t *testing.T) {
	var acc streamAcc
	acc.add(&genai.GenerateContentResponse{Candidates: []*genai.Candidate{{
		Content: &genai.Content{Parts: []*genai.Part{{Text: "Hello ", Thought: true}}},
	}}}, nil)
	acc.add(&genai.GenerateContentResponse{Candidates: []*genai.Candidate{{
		Content: &genai.Content{Parts: []*genai.Part{{Text: "world", Thought: true}}},
	}}}, nil)
	if acc.parts[0].Text != "Hello world" {
		t.Fatalf("got %q", acc.parts[0].Text)
	}
	if acc.answerText() != "" {
		t.Fatal("thoughts should not be answer")
	}
}
