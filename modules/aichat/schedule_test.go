package aichat

import (
	"testing"
	"time"
)

func TestParseScheduleTimeIST(t *testing.T) {
	got, err := parseScheduleTime("2026-11-19T21:30:00")
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 11, 19, 16, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("bare IST parsed as %s, want %s", got.UTC(), want)
	}

	zoned, err := parseScheduleTime("2026-11-19T16:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if !zoned.Equal(want) {
		t.Fatalf("UTC parsed as %s, want %s", zoned.UTC(), want)
	}

	shown := formatIST(zoned)
	if shown != "2026-11-19T21:30:00+05:30" {
		t.Fatalf("formatIST = %s", shown)
	}

	if _, err := parseScheduleTime("tomorrow"); err == nil {
		t.Fatal("expected error for junk time")
	}
}

func TestHasArgIgnoresEmpty(t *testing.T) {
	args := map[string]any{"new_message": "  ", "new_time": "2026-11-19T21:30:00"}
	if hasArg(args, "new_message") {
		t.Fatal("blank new_message should count as not passed")
	}
	if !hasArg(args, "new_time") {
		t.Fatal("new_time should count as passed")
	}
}
