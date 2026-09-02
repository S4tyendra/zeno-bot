package downloader

import (
	"path/filepath"
	"testing"
)

func TestSanitizeFileName(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"video.mp4", "video.mp4"},
		{"  video.mp4  ", "video.mp4"},
		{"../../session.dat", "session.dat"},
		{"/etc/passwd", "passwd"},
		{"foo/bar.mp4", "bar.mp4"},
		{"..", ""},
		{".", ""},
		{"", ""},
		{"100% done.mp4", "100_ done.mp4"},
		{`..\..\session.dat`, "session.dat"},
	}
	for _, tt := range tests {
		if got := sanitizeFileName(tt.in); got != tt.want {
			t.Errorf("sanitizeFileName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestPreserveExt(t *testing.T) {
	if got := preserveExt("clip", "video.mp4"); got != "clip.mp4" {
		t.Fatalf("got %q", got)
	}
	if got := preserveExt("clip.mkv", "video.mp4"); got != "clip.mkv" {
		t.Fatalf("got %q", got)
	}
}

func TestIsMediaSiteURL(t *testing.T) {
	if !isMediaSiteURL("https://www.youtube.com/watch?v=abc") {
		t.Fatal("expected youtube match")
	}
	if !isMediaSiteURL("https://youtu.be/abc") {
		t.Fatal("expected youtu.be match")
	}
	if isMediaSiteURL("https://notyoutube.com/watch") {
		t.Fatal("did not expect notyoutube.com")
	}
	if isMediaSiteURL("https://example.com/?q=youtube.com") {
		t.Fatal("did not expect query-string trap")
	}
	if isMediaSiteURL("https://youtube.com.evil.example/x") {
		t.Fatal("did not expect suffix spoof")
	}
}

func TestIsM3U8URL(t *testing.T) {
	if !isM3U8URL("https://cdn.example.com/live/index.m3u8?token=1") {
		t.Fatal("expected m3u8")
	}
	if !isM3U8URL("https://cdn.example.com/live/INDEX.M3U8") {
		t.Fatal("expected case-insensitive m3u8")
	}
	if isM3U8URL("https://example.com/video.mp4") {
		t.Fatal("did not expect mp4")
	}
}

func TestParseUploadMode(t *testing.T) {
	if parseUploadMode("file") != UploadDocument {
		t.Fatal("file -> doc")
	}
	if parseUploadMode("media") != UploadVideo {
		t.Fatal("media -> video")
	}
	if parseUploadMode("banana") != UploadAuto {
		t.Fatal("unknown -> auto")
	}
}

func TestIsUnderDownloads(t *testing.T) {
	inside := filepath.Join("data", "downloads", "1", "file.mp4")
	if !isUnderDownloads(inside) {
		t.Fatal("expected inside path")
	}
	if isUnderDownloads("session.dat") {
		t.Fatal("did not expect session.dat")
	}
	if isUnderDownloads(filepath.Join("data", "downloads", "..", "session.dat")) {
		t.Fatal("did not expect escaped path")
	}
}
