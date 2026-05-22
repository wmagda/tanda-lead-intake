package email

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

const defaultRequestTimeout = 10 * time.Minute

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

// NewAIClientFromEnv reads env vars and returns a configured Client.
//
// Env vars:
//
//	OPENAI_BASE_URL — e.g. http://localhost:1234/v1
//	OPENAI_MODEL    — model tag loaded in LM Studio (default: "local-model")
//	OPENAI_API_KEY  — required by LM Studio (can be a dummy value like "lm-studio")
//	OPENAI_TIMEOUT  — LLM request timeout, e.g. 10m (default 10m for slow local models)
func NewAIClientFromEnv() *Client {
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

// ParseResult is the deserialized LLM output before domain conversion.
// Matches the flat JSON shape in SystemPrompt; also accepts legacy {"parsed":{...}}.
type ParseResult struct {
	Intent        *string  `json:"intent"`
	DanceStyle    *string  `json:"dance_style"`
	Level         *string  `json:"level"`
	StudentCount  *int     `json:"student_count"`
	RequestedTime *string  `json:"requested_time"`
	NeedsPricing  *bool    `json:"needs_pricing"`
	Confidence    *float64 `json:"ai_confidence"`
	Draft         string   `json:"draft"`
}

// UnmarshalJSON accepts flat prompt output or nested {"parsed":{...},"draft":"..."}.
func (r *ParseResult) UnmarshalJSON(data []byte) error {
	type flat ParseResult
	var f flat
	if err := json.Unmarshal(data, &f); err == nil && (f.Intent != nil || f.Draft != "" || f.DanceStyle != nil) {
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

// validIntents enforces the enum at conversion time.
var validIntents = map[string]bool{
	"private_lesson":   true,
	"group_class":      true,
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
	if f < 0 { return 0 }
	if f > 1 { return 1 }
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

// isEnabled returns true when the client has a BaseURL configured.
func (c *Client) isEnabled() bool {
	return c.BaseURL != "" && strings.HasPrefix(c.BaseURL, "http")
}

// isDisabled is true when the client was constructed with no URL.
func (c *Client) isDisabled() bool { return !c.isEnabled() }

// ParseExtracted sends the email to the local LLM and returns structured lead info + draft.
func (c *Client) ParseExtracted(ctx context.Context, sender, subject, body string) (*models.Lead, string, error) {
	if c.BaseURL == "" {
		return nil, "", fmt.Errorf("AI base URL not configured")
	}

	bodyPreview := body
	if len(bodyPreview) > 4000 {
		bodyPreview = bodyPreview[:4000] + "\n…[TRUNCATED]"
	}

	reqBody := ChatRequest{
		Model:       c.Model,
		Temperature: 0.2,
		MaxTokens:   2048,

		Messages: []ChatMessage{
			{Role: "system", Content: SystemPrompt},
			{Role: "user", Content: UserPrompt(sender, subject, bodyPreview)},
		},
	}

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(reqBody); err != nil {
		return nil, "", fmt.Errorf("encode request: %w", err)
	}

	endpoint := c.BaseURL + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &buf)
	if err != nil {
		return nil, "", fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey := os.Getenv("OPENAI_API_KEY"); apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("http do: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return nil, "", fmt.Errorf("LLM HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var chatResp ChatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return nil, "", fmt.Errorf("decode response: %w", err)
	}

	if chatResp.Error != nil && chatResp.Error.Message != "" {
		return nil, "", fmt.Errorf("LLM error: %s", chatResp.Error.Message)
	}
	if len(chatResp.Choices) == 0 {
		return nil, "", fmt.Errorf("LLM returned no choices")
	}

	choice := chatResp.Choices[0]
	raw, err := extractJSONObject(choiceMessageText(choice))
	if err != nil {
		return nil, "", fmt.Errorf("%w (finish_reason=%q)", err, choice.FinishReason)
	}

	var pr ParseResult
	if err := json.Unmarshal([]byte(raw), &pr); err != nil {
		snip := raw
		if len(snip) > 200 {
			snip = snip[:200] + "…"
		}
		return nil, "", fmt.Errorf("json unmarshal (%q): %w", snip, err)
	}

	lead := pr.ToLead()
	return lead, pr.Draft, nil
}

// StringPtr helper.
func stringPtr(s string) *string { return &s }

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
	// Decode only the first JSON value — models often append prose after the object.
	dec := json.NewDecoder(strings.NewReader(s[start:]))
	var raw json.RawMessage
	if err := dec.Decode(&raw); err != nil {
		return "", fmt.Errorf("decode first JSON object: %w", err)
	}
	return string(raw), nil
}
