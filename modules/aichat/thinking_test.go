package aichat

import (
	"testing"

	"google.golang.org/genai"
)

func TestClampThinking(t *testing.T) {
	cases := []struct {
		model, req string
		want       genai.ThinkingLevel
	}{
		{"gemini-3.8-flash", "minimal", genai.ThinkingLevelLow},
		{"gemini-3.8-flash", "max", genai.ThinkingLevelHigh},
		{"gemini-3.8-flash", "off", genai.ThinkingLevelLow},
		{"gemini-3.7-flash", "medium", genai.ThinkingLevelMedium},
		{"gemini-3.5-flash-lite", "max", genai.ThinkingLevelHigh},
		{"gemini-3.5-flash-lite", "off", genai.ThinkingLevelMinimal},
		{"gemini-3.5-flash-lite", "minimal", genai.ThinkingLevelMinimal},
		{"gemini-3.1-pro", "off", genai.ThinkingLevelLow},
		{"gemini-3.1-pro", "minimal", genai.ThinkingLevelLow},
		{"gemini-3.1-pro", "high", genai.ThinkingLevelHigh},
		{"gemini-3-pro-preview", "medium", genai.ThinkingLevelHigh},
		{"gemini-3-pro-preview", "off", genai.ThinkingLevelLow},
		{"gemini-3.6-flash", "minimal", genai.ThinkingLevelMinimal},
		{"gemini-2.5-flash", "high", ""},
		{"gemini-3.5-flash", "", ""},
	}
	for _, tc := range cases {
		got := clampThinking(tc.model, tc.req)
		if got != tc.want {
			t.Errorf("clampThinking(%q, %q) = %q, want %q", tc.model, tc.req, got, tc.want)
		}
	}
}

func TestParseThoughtHeading(t *testing.T) {
	s := "**Deducing Relationships**\n\nI'm currently working through the initial constraints.\n\n## Checking Pets\n\nCarol owns a dog."
	h, body := parseThoughtHeading(s)
	if h != "Checking Pets" {
		t.Fatalf("heading = %q, want Checking Pets", h)
	}
	if body != "Carol owns a dog." {
		t.Fatalf("body = %q", body)
	}

	h, body = parseThoughtHeading("## first\n\nhello\n\n## second\n\nworld")
	if h != "second" || body != "world" {
		t.Fatalf("got heading=%q body=%q", h, body)
	}
}

func TestNativeMIME(t *testing.T) {
	if !isNativeGeminiMIME(normalizeMIME("image/jpg", "x.jpg")) {
		t.Fatal("jpeg should be native")
	}
	if !isNativeGeminiMIME("video/webm") {
		t.Fatal("webm should be native")
	}
	if isNativeGeminiMIME("application/x-tgsticker") {
		t.Fatal("tgs should not be native")
	}
	if normalizeMIME("application/octet-stream", "clip.mp4") != "video/mp4" {
		t.Fatal("ext fallback")
	}
	if nativeFilePart(&UploadedFile{GoogleFileURI: "gs://aidatax/x", MIMEType: "application/x-tgsticker", FileName: "AnimatedSticker.tgs"}) != nil {
		t.Fatal("tgs must not become FileData")
	}
}

func TestUTF16Slice(t *testing.T) {
	s := "hey 👋 @bot"
	if got := utf16Slice(s, 0, 3); got != "hey" {
		t.Fatalf("prefix = %q", got)
	}
	if got := utf16Slice(s, 4, 2); got != "👋" {
		t.Fatalf("emoji = %q", got)
	}
	if got := utf16Slice(s, 7, 4); got != "@bot" {
		t.Fatalf("mention = %q", got)
	}
}
