package ai

import (
	"fmt"
	"os"
	"strings"
	"sync"
)

// SystemPrompt is injected into every LLM call.
//
// The production/studio-specific prompt lives OUTSIDE the repository in a
// gitignored file (see LoadSystemPrompt). It contains private business details
// (address, phone, pricing, payment links, contact addresses) that must not be
// committed to the public repo. This package loads it at startup from the file
// named by the AI_SYSTEM_PROMPT_FILE env var (default "prompts/intake-system.prompt").
//
// If that file is missing, SystemPrompt falls back to defaultSystemPrompt, a
// generic, business-neutral intake prompt so the worker still functions.
var SystemPrompt = defaultSystemPrompt

// defaultSystemPrompt is a safe generic fallback. It deliberately contains no
// studio-specific facts (no business name, address, phone, pricing, or contact
// addresses) so it is safe to ship in the public repo.
const defaultSystemPrompt = `You are an intake assistant for a dance studio that receives email inquiries about lessons and events.

Return ONLY a JSON object with this exact structure (no markdown, no explanation):

{
  "is_lead": true|false,
  "customer_email": "<visitor email or null>",
  "customer_name": "<visitor name or null>",
  "customer_phone": "<phone number or null>",
  "intent": "private_lesson | group_class | event_booking | pricing | teacher_request | general_question",
  "dance_style": "salsa | bachata | both | other",
  "level": "beginner | intermediate | advanced | not_specified",
  "student_count": <integer or null>,
  "requested_time": "<free-text description of when they want to start, or null>",
  "needs_pricing": true|false,
  "ai_confidence": <float 0.0-1.0>,
  "draft": "<warm, professional reply draft - 1-3 short paragraphs, or empty string if is_lead=false>"
}

Set is_lead=true only when a real person is trying to reach the studio about dancing, classes, lessons, or events. Set is_lead=false for payment/merchant notifications, receipts, marketing, newsletters, calendar invites, password resets, verification codes, auto-replies, and delivery failures. When is_lead=false, set draft="" and other fields may be null.

Return ONLY the JSON object. No markdown, no explanation, no trailing newline after the object.`

var (
	loadPromptOnce sync.Once
	loadedPrompt   string
)

// loadPromptFile reads the external system prompt once. Returns "" if the file
// is not configured or cannot be read, in which case the default is kept.
func loadPromptFile() string {
	loadPromptOnce.Do(func() {
		path := os.Getenv("AI_SYSTEM_PROMPT_FILE")
		if path == "" {
			path = "prompts/intake-system.prompt"
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return
		}
		s := strings.TrimSpace(string(b))
		if s == "" {
			return
		}
		loadedPrompt = s
	})
	return loadedPrompt
}

// LoadSystemPrompt swaps SystemPrompt for the external prompt file content, if
// available. Safe to call repeatedly. Call at startup after env vars are loaded.
func LoadSystemPrompt() {
	if p := loadPromptFile(); p != "" {
		SystemPrompt = p
	}
}

// ResetSystemPromptForTest restores the shipped business-neutral default and
// clears the once-cached file load. Test-only; never used in production paths.
func ResetSystemPromptForTest() {
	loadPromptOnce = sync.Once{}
	loadedPrompt = ""
	SystemPrompt = defaultSystemPrompt
}

// ConversationMessage is one prior turn in the thread (from DB), not the message being parsed.
type ConversationMessage struct {
	Role    string // "customer" or "studio"
	From    string
	Subject string
	Body    string
}

// UserPrompt builds the per-email prompt. prior is optional thread history (oldest first).
func UserPrompt(sender, subject, body string, formRelay, voiceRelay bool, prior []ConversationMessage) string {
	note := ""
	switch {
	case voiceRelay:
		note = "This is a Google Voice SMS/voicemail notification. The envelope From is NOT the customer. " +
			"Extract customer_name and customer_phone from the subject/body (e.g. \"New text message from (269) 290-9011\"). " +
			"Set customer_email to null. Never use [VOICE-NOREPLY] as customer_email. " +
			"In the draft: do not tell them to call; prefer asking for their email or saying the team will follow up by email.\n\n"
	case formRelay:
		note = "The envelope sender is the studio website contact-form relay (e.g. Resend) — extract the visitor's email and name from the body, not from the From header.\n\n"
	}

	history := formatConversationHistory(prior)

	return fmt.Sprintf(`%s%sIncoming email (envelope From) %q with subject %q:

---
%s
---

Return the JSON object now.`, note, history, sender, subject, body)
}

// CalendarSystemSection renders the schedule context for the SYSTEM message
// (appended after SystemPrompt), so it outranks anything in the user message and
// any "Regular schedule" facts listed earlier in the system prompt. Returns ""
// when calendarContext is empty.
func CalendarSystemSection(calendarContext string) string {
	s := strings.TrimSpace(calendarContext)
	if s == "" {
		return ""
	}
	return "\n\n" + s
}

func formatConversationHistory(prior []ConversationMessage) string {
	if len(prior) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Conversation history (oldest first)\n\n")
	b.WriteString("The following messages already happened. Your draft must respond to the **new incoming email below**, not restart the conversation.\n\n")
	for i, m := range prior {
		role := strings.TrimSpace(m.Role)
		if role == "" {
			role = "customer"
		}
		label := "Customer"
		if role == "studio" {
			label = "Studio (sent reply)"
		}
		b.WriteString(fmt.Sprintf("### [%d] %s", i+1, label))
		if m.From != "" {
			b.WriteString(fmt.Sprintf(" — from %q", m.From))
		}
		if m.Subject != "" {
			b.WriteString(fmt.Sprintf(", subject %q", m.Subject))
		}
		b.WriteString("\n\n---\n")
		b.WriteString(truncateBody(m.Body, 2000))
		b.WriteString("\n---\n\n")
	}
	return b.String()
}

func truncateBody(s string, max int) string {
	s = strings.TrimSpace(s)
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max] + "\n…[TRUNCATED]"
}
