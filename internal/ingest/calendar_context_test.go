package ingest

import (
	"strings"
	"testing"
	"time"
)

func TestFormatScheduleContext_AlwaysIncludesToday(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	block := FormatScheduleContext(now, "")
	if !strings.Contains(block, "Wednesday, August 26, 2026") {
		t.Fatalf("expected today's date even with no events, got: %q", block)
	}
	if !strings.Contains(block, "UTC timezone") {
		t.Fatalf("expected timezone in date line, got: %q", block)
	}
	if strings.Contains(block, "Upcoming studio events") {
		t.Fatalf("no events should mean no events section, got: %q", block)
	}
}

func TestFormatScheduleContext_IncludesEventsWithDate(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	events := []UpcomingEvent{
		{Date: time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC), Start: "21:00", Title: "Team Performances at Avos", EventType: "other"},
	}
	block := FormatScheduleContext(now, FormatUpcomingEvents(events))
	if !strings.Contains(block, "Wednesday, August 26, 2026") {
		t.Fatalf("expected today's date before events, got: %q", block)
	}
	if !strings.Contains(block, "Upcoming studio events") || !strings.Contains(block, "Team Performances at Avos") {
		t.Fatalf("expected events section, got: %q", block)
	}
	if strings.Index(block, "Today is") > strings.Index(block, "Upcoming studio events") {
		t.Fatalf("date line should come before events section, got: %q", block)
	}
}

func TestShortTime(t *testing.T) {
	cases := map[string]string{
		"21:00:00": "21:00",
		"21:00":    "21:00",
		"9:05":     "9:05", // already < 5 chars, untouched
		"":         "",
	}
	for in, want := range cases {
		if got := shortTime(in); got != want {
			t.Errorf("shortTime(%q) = %q, want %q", in, got, want)
		}
	}
}
