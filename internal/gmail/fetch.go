package gmail

import (
	"encoding/base64"
	"fmt"
	"log"
	"strings"
	"time"

	gm "google.golang.org/api/gmail/v1"

	"github.com/wmagda/tanda-lead-intake/internal/parseutil"
)

// FetchedMessage is one inbound email extracted from the Gmail API.
type FetchedMessage struct {
	ThreadID  string
	MessageID string
	From      string
	Subject   string
	Body      string
	Date      time.Time
}

// FetchNewMessages lists messages received after `since` and returns parsed messages.
// Skips messages sent by `selfEmail` (outbound).
func FetchNewMessages(svc *gm.Service, selfEmail string, since time.Time) ([]FetchedMessage, error) {
	query := buildQuery(selfEmail, since)
	log.Printf("[gmail] fetching messages: q=%q", query)

	var results []FetchedMessage
	var pageToken string

	for {
		req := svc.Users.Messages.List("me").Q(query).MaxResults(50)
		if pageToken != "" {
			req = req.PageToken(pageToken)
		}
		resp, err := req.Do()
		if err != nil {
			return nil, fmt.Errorf("messages.list: %w", err)
		}

		for _, m := range resp.Messages {
			msg, err := svc.Users.Messages.Get("me", m.Id).Format("full").Do()
			if err != nil {
				log.Printf("[gmail] skip message %s: %v", m.Id, err)
				continue
			}

			fetched := extractMessage(msg)
			if fetched == nil {
				continue
			}

			// Skip messages from ourselves
			_, senderAddr := parseutil.SenderFrom(fetched.From)
			if strings.EqualFold(senderAddr, selfEmail) {
				continue
			}
			results = append(results, *fetched)
		}

		pageToken = resp.NextPageToken
		if pageToken == "" {
			break
		}
	}

	log.Printf("[gmail] fetched %d inbound messages", len(results))
	return results, nil
}

// buildQuery uses Gmail "after:<unix>" so incremental polls don't re-list the whole day.
func buildQuery(selfEmail string, since time.Time) string {
	unix := since.Unix()
	if unix < 0 {
		unix = 0
	}
	q := fmt.Sprintf("after:%d -from:%s", unix, selfEmail)
	if extra := parseutil.IntakeExtraQuery(); extra != "" {
		q = q + " " + extra
	}
	return q
}

func extractMessage(msg *gm.Message) *FetchedMessage {
	if msg.Payload == nil {
		return nil
	}

	headers := headerMap(msg.Payload.Headers)
	from := headers["From"]
	if from == "" {
		return nil
	}

	result := &FetchedMessage{
		ThreadID:  msg.ThreadId,
		MessageID: msg.Id,
		From:      from,
		Subject:   headers["Subject"],
		Date:      time.UnixMilli(msg.InternalDate),
	}

	result.Body = extractBody(msg.Payload)
	return result
}

func headerMap(headers []*gm.MessagePartHeader) map[string]string {
	m := make(map[string]string, len(headers))
	for _, h := range headers {
		m[h.Name] = h.Value
	}
	return m
}

func extractBody(payload *gm.MessagePart) string {
	// Prefer text/plain, fall back to text/html
	if body := findPart(payload, "text/plain"); body != "" {
		return body
	}
	if body := findPart(payload, "text/html"); body != "" {
		return body
	}
	// Single-part message
	if payload.Body != nil && payload.Body.Data != "" {
		decoded, _ := base64.URLEncoding.DecodeString(payload.Body.Data)
		return string(decoded)
	}
	return ""
}

func findPart(part *gm.MessagePart, mimeType string) string {
	if part.MimeType == mimeType && part.Body != nil && part.Body.Data != "" {
		decoded, _ := base64.URLEncoding.DecodeString(part.Body.Data)
		return string(decoded)
	}
	for _, child := range part.Parts {
		if result := findPart(child, mimeType); result != "" {
			return result
		}
	}
	return ""
}
