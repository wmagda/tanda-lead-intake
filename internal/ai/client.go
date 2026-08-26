package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/wmagda/tanda-lead-intake/internal/models"
)

const (
	defaultRequestTimeout = 10 * time.Minute
	defaultRetryMax       = 3
	defaultRetryBase      = 2 * time.Second
)

// ChatMessage represents a single message in the LLM conversation.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatRequest is the body for OpenAI-compatible /v1/chat/completions.
type ChatRequest struct {
	Model       string        `json:"model"`
	Messages    []ChatMessage `json:"messages"`
	Temperature float64       `json:"temperature,omitempty"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
}

// ChatChoice mirrors the subset of the response we read.
type ChatChoice struct {
	Message struct {
		Content          string `json:"content"`
		ReasoningContent string `json:"reasoning_content,omitempty"`
	} `json:"message"`
	FinishReason string `json:"finish_reason,omitempty"`
}

// chatAPIError accepts LM Studio / OpenAI error shapes: string or {"message":"..."}.
type chatAPIError struct {
	Message string
}

func (e *chatAPIError) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	if data[0] == '"' {
		return json.Unmarshal(data, &e.Message)
	}
	var obj struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(data, &obj); err != nil {
		return err
	}
	e.Message = obj.Message
	return nil
}

// ChatResponse is the OpenAI-compatible envelope.
type ChatResponse struct {
	Choices []ChatChoice  `json:"choices"`
	Error   *chatAPIError `json:"error,omitempty"`
}

// Client wraps an LM Studio / compatible OpenAI-endpoint server.
type Client struct {
	BaseURL    string
	Model      string
	HTTPClient *http.Client
}

// NewClientFromEnv reads env vars and returns a configured Client.
//
// Env vars:
//
//	OPENAI_BASE_URL — e.g. http://localhost:1234/v1
//	OPENAI_MODEL    — model tag loaded in LM Studio (default: "local-model")
//	OPENAI_API_KEY  — required by LM Studio (can be a dummy value like "lm-studio")
//	OPENAI_TIMEOUT  — LLM request timeout, e.g. 10m (default 10m for slow local models)
func NewClientFromEnv() *Client {
	timeout := RequestTimeout()
	baseURL := os.Getenv("OPENAI_BASE_URL")
	if baseURL == "" {
		log.Println("[ai] OPENAI_BASE_URL not set — AI parsing disabled")
		return &Client{HTTPClient: &http.Client{Timeout: timeout}}
	}
	baseURL = normalizeBaseURL(baseURL)

	model := os.Getenv("OPENAI_MODEL")
	if model == "" {
		model = "local-model"
	}

	log.Printf("[ai] using %s  model=%q  timeout=%s", baseURL, model, timeout)
	return &Client{
		BaseURL:    baseURL,
		Model:      model,
		HTTPClient: &http.Client{Timeout: timeout},
	}
}

// RequestTimeout returns how long to wait for one LLM completion (env OPENAI_TIMEOUT / AI_TIMEOUT).
func RequestTimeout() time.Duration {
	s := os.Getenv("OPENAI_TIMEOUT")
	if s == "" {
		s = os.Getenv("AI_TIMEOUT")
	}
	if s == "" {
		return defaultRequestTimeout
	}
	if d, err := time.ParseDuration(s); err == nil {
		return d
	}
	if sec, err := strconv.Atoi(s); err == nil && sec > 0 {
		return time.Duration(sec) * time.Second
	}
	log.Printf("[ai] invalid OPENAI_TIMEOUT %q, using %s", s, defaultRequestTimeout)
	return defaultRequestTimeout
}

// RetryMax returns how many times to call the LLM for one email (env OPENAI_RETRY_MAX, default 3).
func RetryMax() int {
	s := strings.TrimSpace(os.Getenv("OPENAI_RETRY_MAX"))
	if s == "" {
		return defaultRetryMax
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		log.Printf("[ai] invalid OPENAI_RETRY_MAX %q, using %d", s, defaultRetryMax)
		return defaultRetryMax
	}
	if n > 10 {
		return 10
	}
	return n
}

func retryBaseDelay() time.Duration {
	s := strings.TrimSpace(os.Getenv("OPENAI_RETRY_BASE"))
	if s == "" {
		return defaultRetryBase
	}
	if d, err := time.ParseDuration(s); err == nil && d > 0 {
		return d
	}
	return defaultRetryBase
}

// ParseResult is the deserialized LLM output before domain conversion.
// Matches the flat JSON shape in SystemPrompt; also accepts legacy {"parsed":{...}}.
type ParseResult struct {
	IsLead        *bool    `json:"is_lead"`
	CustomerEmail *string  `json:"customer_email"`
	CustomerName  *string  `json:"customer_name"`
	CustomerPhone *string  `json:"customer_phone"`
	Intent        *string  `json:"intent"`
	DanceStyle    *string  `json:"dance_style"`
	Level         *string  `json:"level"`
	StudentCount  *int     `json:"student_count"`
	RequestedTime *string  `json:"requested_time"`
	NeedsPricing  *bool    `json:"needs_pricing"`
	Confidence    *float64 `json:"ai_confidence"`
	Draft         string   `json:"draft"`
}

// IsLeadIntent returns whether this message should create a lead (must be explicitly true).
func (r ParseResult) IsLeadIntent() bool {
	return r.IsLead != nil && *r.IsLead
}

// UnmarshalJSON accepts flat prompt output or nested {"parsed":{...},"draft":"..."}.
func (r *ParseResult) UnmarshalJSON(data []byte) error {
	type flat ParseResult
	var f flat
	if err := json.Unmarshal(data, &f); err == nil && (f.Intent != nil || f.Draft != "" || f.DanceStyle != nil || f.IsLead != nil || f.CustomerEmail != nil) {
		*r = ParseResult(f)
		return nil
	}
	var legacy struct {
		Parsed struct {
			Intent        *string  `json:"intent"`
			DanceStyle    *string  `json:"dance_style"`
			Level         *string  `json:"level"`
			StudentCount  *int     `json:"student_count"`
			RequestedTime *string  `json:"requested_time"`
			NeedsPricing  *bool    `json:"needs_pricing"`
			Confidence    *float64 `json:"confidence"`
			AIConfidence  *float64 `json:"ai_confidence"`
		} `json:"parsed"`
		Draft string `json:"draft"`
	}
	if err := json.Unmarshal(data, &legacy); err != nil {
		return err
	}
	r.Intent = legacy.Parsed.Intent
	r.DanceStyle = legacy.Parsed.DanceStyle
	r.Level = legacy.Parsed.Level
	r.StudentCount = legacy.Parsed.StudentCount
	r.RequestedTime = legacy.Parsed.RequestedTime
	r.NeedsPricing = legacy.Parsed.NeedsPricing
	r.Confidence = legacy.Parsed.AIConfidence
	if r.Confidence == nil {
		r.Confidence = legacy.Parsed.Confidence
	}
	r.Draft = legacy.Draft
	return nil
}

var validIntents = map[string]bool{
	"private_lesson":   true,
	"group_class":      true,
	"event_booking":    true,
	"pricing":          true,
	"teacher_request":  true,
	"general_question": true,
}

var validStyles = map[string]bool{
	"salsa": true, "bachata": true, "both": true, "other": true,
}

var validLevels = map[string]bool{
	"beginner": true, "intermediate": true, "advanced": true, "not_specified": true,
}

func clampConfidence(f float64) float64 {
	if f < 0 {
		return 0
	}
	if f > 1 {
		return 1
	}
	return f
}

// ToLead converts ParseResult into a *models.Lead, clamping/validating enums.
func (r ParseResult) ToLead() *models.Lead {
	lead := &models.Lead{}

	if r.Intent != nil {
		v := *r.Intent
		if validIntents[v] {
			lead.RequestType = &v
		} else {
			fallback := "general_question"
			lead.RequestType = &fallback
		}
	}

	if r.DanceStyle != nil {
		v := *r.DanceStyle
		if validStyles[v] {
			lead.DanceStyle = &v
		}
	}

	if r.Level != nil {
		v := *r.Level
		if validLevels[v] {
			lead.Level = &v
		}
	}

	if r.StudentCount != nil {
		n := int32(*r.StudentCount)
		lead.StudentCount = &n
	}

	if r.RequestedTime != nil {
		lead.RequestedTime = r.RequestedTime
	}

	if r.Confidence != nil {
		c := clampConfidence(*r.Confidence)
		lead.AIConfidence = &c
	}

	return lead
}

func (c *Client) isEnabled() bool {
	return c.BaseURL != "" && strings.HasPrefix(c.BaseURL, "http")
}

func (c *Client) isDisabled() bool { return !c.isEnabled() }

// ParseExtracted sends the email to the local LLM and returns structured parse output.
// prior holds earlier thread messages (customer inbound + studio sent drafts) for follow-up context.
// calendarContext is an optional block of upcoming studio events injected into the prompt.
// Transient failures (HTTP 5xx, network, empty choices) are retried with backoff.
func (c *Client) ParseExtracted(ctx context.Context, sender, subject, body string, formRelay, voiceRelay bool, prior []ConversationMessage, calendarContext string) (ParseResult, error) {
	var empty ParseResult
	if c.BaseURL == "" {
		return empty, fmt.Errorf("AI base URL not configured")
	}

	maxAttempts := RetryMax()
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		pr, err := c.parseExtractedOnce(ctx, sender, subject, body, formRelay, voiceRelay, prior, calendarContext)
		if err == nil {
			if attempt > 1 {
				log.Printf("[ai] parse succeeded on attempt %d/%d", attempt, maxAttempts)
			}
			return pr, nil
		}
		lastErr = err
		if !isRetryableParseError(err) || attempt == maxAttempts {
			return empty, err
		}
		delay := retryBaseDelay() * time.Duration(attempt)
		log.Printf("[ai] parse attempt %d/%d failed (retry in %s): %s",
			attempt, maxAttempts, delay, truncateErr(err, 200))
		select {
		case <-ctx.Done():
			return empty, ctx.Err()
		case <-time.After(delay):
		}
	}
	return empty, lastErr
}

func isRetryableParseError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	if strings.Contains(msg, "http do:") {
		return true
	}
	if strings.Contains(msg, "read response:") {
		return true
	}
	if strings.Contains(msg, "LLM HTTP 429") || strings.Contains(msg, "LLM HTTP 5") {
		return true
	}
	if strings.Contains(msg, "LLM returned no choices") {
		return true
	}
	if strings.Contains(msg, "decode response:") {
		return true
	}
	return false
}

func truncateErr(err error, max int) string {
	s := err.Error()
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

func (c *Client) parseExtractedOnce(ctx context.Context, sender, subject, body string, formRelay, voiceRelay bool, prior []ConversationMessage, calendarContext string) (ParseResult, error) {
	var empty ParseResult

	bodyPreview := body
	if len(bodyPreview) > 4000 {
		bodyPreview = bodyPreview[:4000] + "\n…[TRUNCATED]"
	}

	reqBody := ChatRequest{
		Model:       c.Model,
		Temperature: 0.2,
		MaxTokens:   4096,

		Messages: []ChatMessage{
			{Role: "system", Content: SystemPrompt},
			{Role: "user", Content: UserPrompt(sender, subject, bodyPreview, formRelay, voiceRelay, prior, calendarContext)},
		},
	}

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(reqBody); err != nil {
		return empty, fmt.Errorf("encode request: %w", err)
	}

	endpoint := c.BaseURL + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &buf)
	if err != nil {
		return empty, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey := os.Getenv("OPENAI_API_KEY"); apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return empty, fmt.Errorf("http do: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return empty, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode >= http.StatusBadRequest {
		snip := strings.TrimSpace(string(respBody))
		if len(snip) > 300 {
			snip = snip[:300] + "…"
		}
		return empty, fmt.Errorf("LLM HTTP %d: %s", resp.StatusCode, snip)
	}

	var chatResp ChatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return empty, fmt.Errorf("decode response: %w", err)
	}

	if chatResp.Error != nil && chatResp.Error.Message != "" {
		return empty, fmt.Errorf("LLM error: %s", chatResp.Error.Message)
	}
	if len(chatResp.Choices) == 0 {
		return empty, fmt.Errorf("LLM returned no choices")
	}

	choice := chatResp.Choices[0]
	raw, err := extractJSONObject(choiceMessageText(choice))
	if err != nil {
		return empty, fmt.Errorf("%w (finish_reason=%q)", err, choice.FinishReason)
	}

	var pr ParseResult
	if err := json.Unmarshal([]byte(raw), &pr); err != nil {
		snip := raw
		if len(snip) > 200 {
			snip = snip[:200] + "…"
		}
		return empty, fmt.Errorf("json unmarshal (%q): %w", snip, err)
	}

	return pr, nil
}

func normalizeBaseURL(u string) string {
	u = strings.TrimRight(u, "/")
	if !strings.HasSuffix(u, "/v1") {
		u += "/v1"
	}
	return u
}

func choiceMessageText(c ChatChoice) string {
	if s := strings.TrimSpace(c.Message.Content); s != "" {
		return s
	}
	return strings.TrimSpace(c.Message.ReasoningContent)
}

func extractJSONObject(s string) (string, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	s = strings.TrimSpace(s)
	if s == "" {
		return "", fmt.Errorf("empty model output")
	}
	start := strings.IndexByte(s, '{')
	if start < 0 {
		return "", fmt.Errorf("no JSON object in model output (%d chars)", len(s))
	}
	dec := json.NewDecoder(strings.NewReader(s[start:]))
	var raw json.RawMessage
	if err := dec.Decode(&raw); err != nil {
		return "", fmt.Errorf("decode first JSON object: %w", err)
	}
	return string(raw), nil
}
