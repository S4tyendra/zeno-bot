package aichat

import "testing"

func TestStripStatusNoise(t *testing.T) {
	if !isJunkBotMessage("📤 Sending file…") {
		t.Fatal("status line should be junk")
	}
	got := stripStatusNoise("my bad\n📤 Sending file…\npricing is up")
	if got != "my bad\npricing is up" {
		t.Fatalf("got %q", got)
	}
	if stripStatusNoise("Sending file") != "" {
		t.Fatal("bare status should drop")
	}
}
