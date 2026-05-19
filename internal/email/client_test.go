package email_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/wmagda/tanda-lead-intake/internal/email"
	"github.com/wmagda/tanda-lead-intake/internal/models"
)

func intPtr(n int) *int       { return &n }
func boolPtr(b bool) *bool    { return &b }
func stringPtr(s string) *string { return &s }

// ── toLead conversion ────────────────────────────────────────────────

func minimalParseResult() *email.ParseResult {
	i := "private_lesson"
	d := "salsa"
	l := "beginner"
	c := 0.96
	return &email.ParseResult{
		Parsed: struct {
			Intent        *string  `json:"intent"`
			DanceStyle    *string  `json:"dance_style"`
			Level         *string  `json:"level"`
			StudentCount  *int     `json:"student_count"`
			RequestedTime *string  `json:"requested_time"`
			NeedsPricing  *bool    `json:"needs_pricing"`
			Confidence    *float64 `json:"confidence"`
		}{
			Intent:        &i,
			DanceStyle:    &d,
			Level:         &l,
			StudentCount:  intPtr(2),
			RequestedTime: stringPtr("Tuesday evening"),
			NeedsPricing:  boolPtr(true),
			Confidence:    &c,
		},
		Draft: "Hi! Thanks for reaching out to Salsa Collective.",
	}
}

func TestParseResult_toLead_ValidIntent(t *testing.T) {
	pr := minimalParseResult()
	lead := pr.toLead()
	if lead.RequestType == nil || *lead.RequestType != "private_lesson" {
		t.Fatalf("expected intent private_lesson, got %v", lead.RequestType)
	}
}

func TestParseResult_toLead_InvalidIntentFallsBack(t *testing.T) {
	i := "totally_bogus"
	pr := &email.ParseResult{
		Parsed: struct {
			Intent        *string  `json:"intent"`
			DanceStyle    *string  `json:"dance_style"`
			Level         *string  `json:"level"`
			StudentCount  *int     `json:"student_count"`
			RequestedTime *string  `json:"requested_time"`
			NeedsPricing  *bool    `json:"needs_pricing"`
			Confidence    *float64 `json:"confidence"`
		}{Intent: &i},
		Draft: "test",
	}
	lead := pr.toLead()
	if lead.RequestType == nil || *lead.RequestType != "general_question" {
		t.Fatalf("expected fallback general_question, got %v", lead.RequestType)
	}
}

func TestParseResult_toLead_AllValidIntents(t *testing.T) {
	intents := []string{"private_lesson", "group_class", "pricing", "teacher_request", "general_question"}
	for _, intent := range intents {
		t.Run(intent, func(t *testing.T) {
			pr := minimalParseResult()
			pr.Parsed.Intent = &intent
			lead := pr.toLead()
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
			pr.Parsed.DanceStyle = &s
			lead := pr.toLead()
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
			pr.Parsed.Level = &l
			lead := pr.toLead()
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
		pr.Parsed.Confidence = &c
		lead := pr.toLead()
		if *lead.AIConfidence != 0.75 {
			t.Fatalf("expected 0.75, got %v", *lead.AIConfidence)
		}
	})
	t.Run("above 1.0", func(t *testing.T) {
		pr := minimalParseResult()
		c := 1.5
		pr.Parsed.Confidence = &c
		lead := pr.toLead()
		if *lead.AIConfidence != 1.0 {
			t.Fatalf("expected 1.0, got %v", *lead.AIConfidence)
		}
	})
	t.Run("below 0.0", func(t *testing.T) {
		pr := minimalParseResult()
		c := -0.3
		pr.Parsed.Confidence = &c
		lead := pr.toLead()
		if *lead.AIConfidence != 0.0 {
			t.Fatalf("expected 0.0, got %v", *lead.AIConfidence)
		}
	})
}

func TestParseResult_toLead_OptionalFieldsNil(t *testing.T) {
	pr := &email.ParseResult{
		Parsed: struct {
			Intent        *string  `json:"intent"`
			DanceStyle    *string  `json:"dance_style"`
			Level         *string  `json:"level"`
			StudentCount  *int     `json:"student_count"`
			RequestedTime *string  `json:"requested_time"`
			NeedsPricing  *bool    `json:"needs_pricing"`
			Confidence    *float64 `json:"confidence"`
		}{},
		Draft: "",
	}
	lead := pr.toLead()
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

// ── SystemPrompt integrity ───────────────────────────────────────────

func TestSystemPrompt_ValidUTF8(t *testing.T) {
	if !utf8.ValidString(email.SystemPrompt) {
		t.Fatal("SystemPrompt contains invalid UTF-8")
	}
}

func TestSystemPrompt_SizeOk(t *testing.T) {
	if len(email.SystemPrompt) > 12_000 {
		t.Fatalf("SystemPrompt is %d bytes — too large for most context windows", len(email.SystemPrompt))
	}
}

func TestSystemPrompt_ContainsRequiredKeywords(t *testing.T) {
	lower := strings.ToLower(email.SystemPrompt)
	required := []string{
		"private_lesson", "group_class", "pricing",
		"beginner", "intermediate", "advanced",
		"salsa", "bachata",
		"[STUDIO-EMAIL]",
		"[STUDIO-ADDRESS]",
		"$10",
	}
	for _, kw := range required {
		if !strings.Contains(lower, strings.ToLower(kw)) {
			t.Fatalf("SystemPrompt missing keyword: %s", kw)
		}
	}
}

// ── UserPrompt format ───────────────────────────────────────────────

func TestUserPrompt_ContainsSenderAndSubject(t *testing.T) {
	p := email.UserPrompt("alice@example.com", "Hello", "body text")
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
