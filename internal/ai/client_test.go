package ai_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/wmagda/tanda-lead-intake/internal/ai"
)

func intPtr(n int) *int          { return &n }
func boolPtr(b bool) *bool       { return &b }
func stringPtr(s string) *string { return &s }

func minimalParseResult() *ai.ParseResult {
	i := "private_lesson"
	d := "salsa"
	l := "beginner"
	c := 0.96
	return &ai.ParseResult{
		Intent:        &i,
		DanceStyle:    &d,
		Level:         &l,
		StudentCount:  intPtr(2),
		RequestedTime: stringPtr("Tuesday evening"),
		NeedsPricing:  boolPtr(true),
		Confidence:    &c,
		Draft:         "Hi! Thanks for reaching out to Salsa Collective.",
	}
}

func TestParseResult_toLead_ValidIntent(t *testing.T) {
	pr := minimalParseResult()
	lead := pr.ToLead()
	if lead.RequestType == nil || *lead.RequestType != "private_lesson" {
		t.Fatalf("expected intent private_lesson, got %v", lead.RequestType)
	}
}

func TestParseResult_toLead_InvalidIntentFallsBack(t *testing.T) {
	i := "totally_bogus"
	pr := &ai.ParseResult{Intent: &i, Draft: "test"}
	lead := pr.ToLead()
	if lead.RequestType == nil || *lead.RequestType != "general_question" {
		t.Fatalf("expected fallback general_question, got %v", lead.RequestType)
	}
}

func TestParseResult_toLead_AllValidIntents(t *testing.T) {
	intents := []string{"private_lesson", "group_class", "event_booking", "pricing", "teacher_request", "general_question"}
	for _, intent := range intents {
		t.Run(intent, func(t *testing.T) {
			pr := minimalParseResult()
			pr.Intent = &intent
			lead := pr.ToLead()
			if lead.RequestType == nil || *lead.RequestType != intent {
				t.Fatalf("expected %s, got %v", intent, lead.RequestType)
			}
		})
	}
}

func TestParseResult_toLead_AllValidStyles(t *testing.T) {
	styles := []string{"salsa", "bachata", "both", "other"}
	for _, s := range styles {
		t.Run(s, func(t *testing.T) {
			pr := minimalParseResult()
			pr.DanceStyle = &s
			lead := pr.ToLead()
			if lead.DanceStyle == nil || *lead.DanceStyle != s {
				t.Fatalf("expected %s, got %v", s, lead.DanceStyle)
			}
		})
	}
}

func TestParseResult_toLead_AllValidLevels(t *testing.T) {
	levels := []string{"beginner", "intermediate", "advanced", "not_specified"}
	for _, l := range levels {
		t.Run(l, func(t *testing.T) {
			pr := minimalParseResult()
			pr.Level = &l
			lead := pr.ToLead()
			if lead.Level == nil || *lead.Level != l {
				t.Fatalf("expected %s, got %v", l, lead.Level)
			}
		})
	}
}

func TestParseResult_toLead_ConfidenceClamped(t *testing.T) {
	t.Run("within range", func(t *testing.T) {
		pr := minimalParseResult()
		c := 0.75
		pr.Confidence = &c
		lead := pr.ToLead()
		if *lead.AIConfidence != 0.75 {
			t.Fatalf("expected 0.75, got %v", *lead.AIConfidence)
		}
	})
	t.Run("above 1.0", func(t *testing.T) {
		pr := minimalParseResult()
		c := 1.5
		pr.Confidence = &c
		lead := pr.ToLead()
		if *lead.AIConfidence != 1.0 {
			t.Fatalf("expected 1.0, got %v", *lead.AIConfidence)
		}
	})
	t.Run("below 0.0", func(t *testing.T) {
		pr := minimalParseResult()
		c := -0.3
		pr.Confidence = &c
		lead := pr.ToLead()
		if *lead.AIConfidence != 0.0 {
			t.Fatalf("expected 0.0, got %v", *lead.AIConfidence)
		}
	})
}

func TestParseResult_toLead_OptionalFieldsNil(t *testing.T) {
	pr := &ai.ParseResult{Draft: ""}
	lead := pr.ToLead()
	if lead.StudentCount != nil {
		t.Fatalf("expected nil student_count, got %v", *lead.StudentCount)
	}
	if lead.RequestedTime != nil {
		t.Fatalf("expected nil requested_time, got %v", *lead.RequestedTime)
	}
	if lead.DanceStyle != nil {
		t.Fatalf("expected nil dance_style, got %v", *lead.DanceStyle)
	}
}

func TestParseResult_Unmarshal_FlatPromptShape(t *testing.T) {
	raw := `{
		"intent": "private_lesson",
		"dance_style": "salsa",
		"level": "beginner",
		"student_count": 2,
		"ai_confidence": 0.9,
		"draft": "Hello!"
	}`
	var pr ai.ParseResult
	if err := json.Unmarshal([]byte(raw), &pr); err != nil {
		t.Fatal(err)
	}
	if pr.Intent == nil || *pr.Intent != "private_lesson" {
		t.Fatalf("intent: %v", pr.Intent)
	}
	if pr.Draft != "Hello!" {
		t.Fatalf("draft: %q", pr.Draft)
	}
}

func TestSystemPrompt_ValidUTF8(t *testing.T) {
	if !utf8.ValidString(ai.SystemPrompt) {
		t.Fatal("SystemPrompt contains invalid UTF-8")
	}
}

func TestSystemPrompt_SizeOk(t *testing.T) {
	if len(ai.SystemPrompt) > 12_000 {
		t.Fatalf("SystemPrompt is %d bytes — too large for most context windows", len(ai.SystemPrompt))
	}
}

// The in-repo default must stay business-neutral (no studio-specific facts),
// because it ships in the public repo. Real business details live in the
// gitignored prompts/ file loaded by LoadSystemPrompt.
func TestDefaultSystemPrompt_BusinessNeutral(t *testing.T) {
	// Reset to the shipped default (a previous test may have loaded the file).
	ai.ResetSystemPromptForTest()
	lower := strings.ToLower(ai.SystemPrompt)
	for _, kw := range []string{
		"@gmail.com", // no real contact address
		"970",        // no real phone area code
		"salsa collective", // no business name
		"square.link",      // no payment link
		"drake",            // no street address
	} {
		if strings.Contains(lower, kw) {
			t.Fatalf("default SystemPrompt must be business-neutral but contains %q", kw)
		}
	}
	// But it must still carry the JSON contract the parser depends on.
	for _, kw := range []string{"is_lead", "customer_email", "intent", "draft", "private_lesson"} {
		if !strings.Contains(lower, kw) {
			t.Fatalf("default SystemPrompt missing required JSON keyword: %s", kw)
		}
	}
}

// LoadSystemPrompt swaps in the external prompt file when present, and keeps
// the default otherwise.
func TestLoadSystemPrompt_LoadsFileWhenPresent(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/intake.prompt"
	if err := os.WriteFile(path, []byte("CUSTOM INTAKE PROMPT BODY\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AI_SYSTEM_PROMPT_FILE", path)

	ai.ResetSystemPromptForTest()
	ai.LoadSystemPrompt()
	if !strings.Contains(ai.SystemPrompt, "CUSTOM INTAKE PROMPT BODY") {
		t.Fatalf("LoadSystemPrompt did not load external file; got: %.80s", ai.SystemPrompt)
	}
	ai.ResetSystemPromptForTest()
}

func TestLoadSystemPrompt_KeepsDefaultWhenFileMissing(t *testing.T) {
	t.Setenv("AI_SYSTEM_PROMPT_FILE", "/nonexistent/intake.prompt")
	ai.ResetSystemPromptForTest()
	ai.LoadSystemPrompt()
	if !strings.Contains(ai.SystemPrompt, "intake assistant") {
		t.Fatalf("expected default prompt retained, got: %.80s", ai.SystemPrompt)
	}
	ai.ResetSystemPromptForTest()
}

func TestParseResult_IsLeadIntent(t *testing.T) {
	falseVal := false
	if (&ai.ParseResult{IsLead: &falseVal}).IsLeadIntent() {
		t.Fatal("expected false")
	}
	trueVal := true
	if !(&ai.ParseResult{IsLead: &trueVal}).IsLeadIntent() {
		t.Fatal("expected true")
	}
	if (&ai.ParseResult{}).IsLeadIntent() {
		t.Fatal("nil is_lead should be false")
	}
}

func TestUserPrompt_ContainsSenderAndSubject(t *testing.T) {
	p := ai.UserPrompt("alice@example.com", "Hello", "body text", false, false, nil)
	if !strings.Contains(p, "alice@example.com") {
		t.Error("UserPrompt should contain sender email")
	}
	if !strings.Contains(p, "Hello") {
		t.Error("UserPrompt should contain subject")
	}
	if !strings.Contains(p, "body text") {
		t.Error("UserPrompt should contain body")
	}
}

func TestUserPrompt_IncludesConversationHistory(t *testing.T) {
	prior := []ai.ConversationMessage{
		{Role: "customer", From: "bob@example.com", Subject: "Lessons?", Body: "We want salsa lessons"},
		{Role: "studio", Body: "Thanks for reaching out! Private lessons are [SOLO-RATE]."},
	}
	p := ai.UserPrompt("bob@example.com", "Re: Lessons?", "Tuesday works", false, false, prior)
	if !strings.Contains(p, "Conversation history") {
		t.Fatal("expected conversation history section")
	}
	if !strings.Contains(p, "We want salsa lessons") {
		t.Fatal("expected prior customer message")
	}
	if !strings.Contains(p, "Private lessons are [SOLO-RATE]") {
		t.Fatal("expected prior studio reply")
	}
	if !strings.Contains(p, "Tuesday works") {
		t.Fatal("expected current message")
	}
}

func TestCalendarSystemSection_AppendsCalendar(t *testing.T) {
	cal := "## Upcoming studio events (schedule context)\nMon 2026-09-11 9:00 PM - 10:00 PM · Team Performances at Avos [other]"
	section := ai.CalendarSystemSection(cal)
	if !strings.HasPrefix(section, "\n\n") {
		t.Fatalf("expected leading newlines before calendar section, got: %q", section)
	}
	if !strings.Contains(section, "Team Performances at Avos") {
		t.Fatalf("expected calendar content in section, got: %q", section)
	}
}

func TestCalendarSystemSection_EmptyReturnsEmpty(t *testing.T) {
	if s := ai.CalendarSystemSection(""); s != "" {
		t.Fatalf("empty calendar context should yield empty section, got: %q", s)
	}
	if s := ai.CalendarSystemSection("   \n "); s != "" {
		t.Fatalf("whitespace-only calendar context should yield empty section, got: %q", s)
	}
}
