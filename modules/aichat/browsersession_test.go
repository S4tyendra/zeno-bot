package aichat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBrowserStateNote(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PROFILES_DIR", dir)
	day := time.Now().In(ist).Format("2006-01-02")
	dayDir := filepath.Join(dir, "7", day)
	if err := os.MkdirAll(dayDir, 0700); err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"current":"https://example.com/","tabs":[{"url":"https://example.com/","title":"Example"}]}`)
	if err := os.WriteFile(filepath.Join(dayDir, "tabs.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	note := browserStateNote(true, 7, 7)
	if !strings.Contains(note, "https://example.com/") || !strings.Contains(note, "private") {
		t.Fatalf("note: %s", note)
	}
	group := browserStateNote(false, 99, 7)
	if !strings.Contains(group, "shared") || strings.Contains(group, "example.com") {
		t.Fatalf("group note should be empty of the private tabs: %s", group)
	}
}
