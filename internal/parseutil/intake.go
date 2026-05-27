package parseutil

import (
	"log"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func envCommaList(key string) []string {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if a := strings.ToLower(strings.TrimSpace(part)); a != "" {
			out = append(out, a)
		}
	}
	return out
}

// FormRelayFrom returns addresses from GMAIL_FORM_FROM (website contact form relay, e.g. Resend).
func FormRelayFrom() []string {
	return envCommaList("GMAIL_FORM_FROM")
}

// IntakeExtraQuery returns optional extra Gmail search terms from GMAIL_INTAKE_QUERY.
func IntakeExtraQuery() string {
	return strings.TrimSpace(os.Getenv("GMAIL_INTAKE_QUERY"))
}

// InitialLookback is how far back the worker searches on first start (env GMAIL_INITIAL_LOOKBACK).
// Examples: 24h, 168h, 7d. Default 24h. Only applies when the process starts; later polls are incremental.
func InitialLookback() time.Duration {
	const defaultLookback = 24 * time.Hour
	s := strings.TrimSpace(os.Getenv("GMAIL_INITIAL_LOOKBACK"))
	if s == "" {
		return defaultLookback
	}
	if strings.HasSuffix(s, "d") {
		n, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err == nil && n > 0 {
			return time.Duration(n) * 24 * time.Hour
		}
	}
	if d, err := time.ParseDuration(s); err == nil && d > 0 {
		return d
	}
	log.Printf("[gmail] invalid GMAIL_INITIAL_LOOKBACK %q, using %s", s, defaultLookback)
	return defaultLookback
}

func matchesAddrList(addr string, list []string) bool {
	addr = strings.ToLower(strings.TrimSpace(addr))
	for _, a := range list {
		if addr == a {
			return true
		}
	}
	return false
}

// IsFormRelay is true when the envelope From is a known website form relay.
func IsFormRelay(envelopeFrom string) bool {
	_, email := SenderFrom(envelopeFrom)
	return matchesAddrList(email, FormRelayFrom())
}

var (
	reEmailField = regexp.MustCompile(`(?im)^\s*(?:e-?mail|email\s+address)\s*:\s*<?([^\s<>]+@[^\s>]+)>?\s*$`)
	reMailto     = regexp.MustCompile(`(?i)([a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,})`)
	rePhone      = regexp.MustCompile(`(?:\+?1[-.\s]?)?\(?([0-9]{3})\)?[-.\s]?([0-9]{3})[-.\s]?([0-9]{4})`)

	reVoiceMsgFrom = regexp.MustCompile(`(?i)new (?:google voice )?(?:text )?message from\s+(.+)`)
	reVoiceFrom    = regexp.MustCompile(`(?i)(?:missed call|voicemail|text message|message) from\s+(.+)`)
)

// Default Google Voice notification senders (envelope is not the caller).
var googleVoiceSenders = []string{
	"[VOICE-NOREPLY]",
	"sms-noreply@google.com",
}

// IsGoogleVoiceRelay detects Gmail notifications from Google Voice (SMS/voicemail forwards).
func IsGoogleVoiceRelay(envelopeFrom, subject, body string) bool {
	_, email := SenderFrom(envelopeFrom)
	for _, s := range googleVoiceSenders {
		if email == s {
			return true
		}
	}
	combined := strings.ToLower(subject + "\n" + body)
	if len(combined) > 2000 {
		combined = combined[:2000]
	}
	return strings.Contains(combined, "google voice") ||
		strings.Contains(strings.ToLower(subject), "new text message from") ||
		strings.Contains(strings.ToLower(subject), "new voicemail")
}

// IsNotificationSenderEmail is true for relay/system addresses that must not be stored as customer_email.
func IsNotificationSenderEmail(email string) bool {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return false
	}
	for _, s := range googleVoiceSenders {
		if email == s {
			return true
		}
	}
	if matchesAddrList(email, FormRelayFrom()) {
		return true
	}
	return strings.Contains(email, "noreply") ||
		strings.HasSuffix(email, "@business.facebook.com")
}

// IsGoogleVoiceDisplayName is the generic From display name on Voice notifications.
func IsGoogleVoiceDisplayName(name string) bool {
	return strings.EqualFold(strings.TrimSpace(name), "Google Voice")
}

// ExtractGoogleVoiceContact parses caller name/phone from Voice SMS/voicemail notification subject+body.
func ExtractGoogleVoiceContact(subject, body string) (name, phone string) {
	phone = ExtractPhoneFromBody(subject + "\n" + body)

	tryLine := func(line string) {
		line = strings.TrimSpace(line)
		if line == "" {
			return
		}
		if m := reVoiceMsgFrom.FindStringSubmatch(line); len(m) > 1 {
			parseVoiceFromClause(strings.TrimSpace(m[1]), &name, &phone)
			return
		}
		if m := reVoiceFrom.FindStringSubmatch(line); len(m) > 1 {
			parseVoiceFromClause(strings.TrimSpace(m[1]), &name, &phone)
		}
	}

	tryLine(subject)
	if name == "" && phone == "" {
		for _, line := range strings.Split(body, "\n") {
			tryLine(line)
			if name != "" || phone != "" {
				break
			}
		}
	}

	if IsGoogleVoiceDisplayName(name) {
		name = ""
	}
	return name, phone
}

func parseVoiceFromClause(clause string, name, phone *string) {
	clause = strings.TrimSpace(clause)
	if clause == "" {
		return
	}
	if p := ExtractPhoneFromBody(clause); p != "" {
		if *phone == "" {
			*phone = p
		}
		// Name before phone: "Jane Doe (269) 290-9011"
		before := strings.TrimSpace(strings.Split(clause, "(")[0])
		if before != "" && !IsGoogleVoiceDisplayName(before) && !strings.Contains(before, ")") {
			*name = before
		}
		return
	}
	if !IsGoogleVoiceDisplayName(clause) {
		*name = clause
	}
}

// ExtractContactFromFormBody tries to read the visitor's name/email from a contact-form body.
func ExtractContactFromFormBody(body string) (name, email string) {
	for _, line := range strings.Split(body, "\n") {
		if m := reEmailField.FindStringSubmatch(line); len(m) > 1 {
			email = strings.ToLower(strings.TrimSpace(m[1]))
			break
		}
	}
	if email == "" {
		for _, m := range reMailto.FindAllStringSubmatch(body, -1) {
			candidate := strings.ToLower(m[1])
			if !strings.Contains(candidate, "[STUDIO-SLUG]") &&
				!strings.HasSuffix(candidate, "wordpress.com") {
				email = candidate
				break
			}
		}
	}
	for _, line := range strings.Split(body, "\n") {
		lower := strings.ToLower(strings.TrimSpace(line))
		if strings.HasPrefix(lower, "name:") {
			name = strings.TrimSpace(line[strings.Index(line, ":")+1:])
			break
		}
	}
	return name, email
}

// ExtractPhoneFromBody returns the first US-style phone number found in text.
func ExtractPhoneFromBody(body string) string {
	if m := rePhone.FindStringSubmatch(body); len(m) > 0 {
		return strings.TrimSpace(m[0])
	}
	return ""
}
