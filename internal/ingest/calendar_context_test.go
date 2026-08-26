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
		{Date: time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC), Start: "21:00", End: "22:00", Title: "Team Performances at Avos", EventType: "other"},
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

func TestFormatUpcomingEvents_AmPmTimes(t *testing.T) {
	events := []UpcomingEvent{
		{Date: time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC), Start: "18:30", End: "20:30", Title: "NO CLASSES - Summer Break!", EventType: "salsa", FromSeries: true},
		{Date: time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC), Start: "21:00", End: "22:00", Title: "Team Performances at Avos", EventType: "other"},
		{Date: time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC), Start: "09:05", End: "09:05", Title: "Early event", EventType: "other"},
	}
	block := FormatUpcomingEvents(events)
	for _, want := range []string{"6:30 PM - 8:30 PM", "9:00 PM - 10:00 PM", "9:05 AM"} {
		if !strings.Contains(block, want) {
			t.Errorf("expected AM/PM time %q in block, got:\n%s", want, block)
		}
	}
	if strings.Contains(block, "18:30") || strings.Contains(block, "21:00") {
		t.Errorf("24h times should not appear, got:\n%s", block)
	}
}

func TestFormatUpcomingEvents_AuthoritativeInstruction(t *testing.T) {
	events := []UpcomingEvent{
		{Date: time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC), Start: "21:00", Title: "Social", EventType: "other"},
	}
	block := FormatUpcomingEvents(events)
	if !strings.Contains(block, "authoritative") {
		t.Errorf("expected authoritative instruction in block, got:\n%s", block)
	}
	if !strings.Contains(block, "Regular schedule") {
		t.Errorf("expected override of 'Regular schedule' instruction, got:\n%s", block)
	}
}

func TestFormatAmPm(t *testing.T) {
	cases := map[string]string{
		"18:30":    "6:30 PM",
		"21:00":    "9:00 PM",
		"09:05":    "9:05 AM",
		"00:00":    "12:00 AM",
		"12:15":    "12:15 PM",
		"21:00:00": "9:00 PM",
		"":         "",
		"junk":     "junk", // unparseable: returned unchanged
	}
	for in, want := range cases {
		if got := formatAmPm(in); got != want {
			t.Errorf("formatAmPm(%q) = %q, want %q", in, got, want)
		}
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
