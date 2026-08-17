package parseutil

import (
	"os"
	"testing"
	"time"
)

func TestExtractContactFromFormBody(t *testing.T) {
	body := `New contact form submission

Name: Jane Doe
Email: jane@example.com

Message:
I'd like a private lesson on Tuesday.`
	name, email := ExtractContactFromFormBody(body)
	if email != "jane@example.com" {
		t.Fatalf("email=%q", email)
	}
	if name != "Jane Doe" {
		t.Fatalf("name=%q", name)
	}
}

func TestIsFormRelay(t *testing.T) {
	os.Setenv("GMAIL_FORM_FROM", "[FORM-RELAY]")
	defer os.Unsetenv("GMAIL_FORM_FROM")

	if !IsFormRelay("Resend <[FORM-RELAY]>") {
		t.Fatal("expected form relay")
	}
	if IsFormRelay("Jane <jane@example.com>") {
		t.Fatal("expected not form relay")
	}
}

func TestInitialLookback(t *testing.T) {
	os.Setenv("GMAIL_INITIAL_LOOKBACK", "7d")
	defer os.Unsetenv("GMAIL_INITIAL_LOOKBACK")
	if got := InitialLookback(); got != 7*24*time.Hour {
		t.Fatalf("got %v", got)
	}
}

func TestEnvDuration(t *testing.T) {
	const key = "TEST_POLL_INTERVAL"
	os.Setenv(key, "45s")
	defer os.Unsetenv(key)
	if got := EnvDuration(key, time.Minute); got != 45*time.Second {
		t.Fatalf("got %v", got)
	}
	if got := EnvDuration("TEST_UNSET_INTERVAL", time.Minute); got != time.Minute {
		t.Fatalf("default: got %v", got)
	}
}

func TestExtractPhoneFromBody(t *testing.T) {
	body := "New Google Voice message from (970) 555-1234"
	if got := ExtractPhoneFromBody(body); got == "" {
		t.Fatal("expected phone")
	}
}

func TestIsStudioPhone(t *testing.T) {
	// Studio phone comes from STUDIO_PHONE env; use a placeholder in tests.
	t.Setenv("STUDIO_PHONE", "(555) 010-9999")
	for _, p := range []string{"(555) 010-9999", "555-010-9999", "+1 555 010 9999"} {
		if !IsStudioPhone(p) {
			t.Fatalf("expected studio phone %q", p)
		}
	}
	if IsStudioPhone("(269) 290-9011") {
		t.Fatal("customer phone should not match studio")
	}
	if IsStudioPhone("") {
		t.Fatal("empty should not match")
	}
}

func TestIsStudioPhone_NoEnvNoMatch(t *testing.T) {
	// With no STUDIO_PHONE set, nothing is treated as the studio line.
	t.Setenv("STUDIO_PHONE", "")
	if IsStudioPhone("(555) 010-9999") {
		t.Fatal("with no STUDIO_PHONE, nothing should match")
	}
}

func TestExtractPhoneFromBody_skipsStudio(t *testing.T) {
	t.Setenv("STUDIO_PHONE", "(555) 010-9999")
	body := "Thanks!\n\nSalsa Collective\n(555) 010-9999\n\nOn Mon, Jane wrote:\nMy number is (269) 290-9011"
	got := ExtractPhoneFromBody(body)
	if got == "" || IsStudioPhone(got) {
		t.Fatalf("expected customer phone, got %q", got)
	}
	if NormalizePhoneDigits(got) != "2692909011" {
		t.Fatalf("got %q", got)
	}
}

func TestIsGoogleVoiceRelay(t *testing.T) {
	if !IsGoogleVoiceRelay("Google Voice <[VOICE-NOREPLY]>", "New text message from (269) 290-9011", "") {
		t.Fatal("expected voice relay")
	}
}

func TestExtractGoogleVoiceContact_phoneOnly(t *testing.T) {
	name, phone := ExtractGoogleVoiceContact("New text message from (269) 290-9011", "Message body")
	if phone == "" {
		t.Fatalf("phone=%q", phone)
	}
	if name != "" {
		t.Fatalf("name should be empty for phone-only subject, got %q", name)
	}
}

func TestExtractGoogleVoiceContact_nameAndPhone(t *testing.T) {
	name, phone := ExtractGoogleVoiceContact("New text message from Jane Doe (269) 290-9011", "")
	if phone == "" || name != "Jane Doe" {
		t.Fatalf("name=%q phone=%q", name, phone)
	}
}

func TestIsNotificationSenderEmail(t *testing.T) {
	if !IsNotificationSenderEmail("[VOICE-NOREPLY]") {
		t.Fatal("expected notification sender")
	}
	if IsNotificationSenderEmail("jane@example.com") {
		t.Fatal("expected real customer email")
	}
}
