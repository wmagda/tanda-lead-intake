package email

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/wmagda/tanda-lead-intake/internal/models"
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
		Content string `json:"content"`
	} `json:"message"`
}

// ChatResponse is the OpenAI-compatible envelope.
type ChatResponse struct {
	Choices []ChatChoice `json:"choices"`
	Error   *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
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
func NewAIClientFromEnv() *Client {
	baseURL := os.Getenv("OPENAI_BASE_URL")
	if baseURL == "" {
		log.Println("[ai] OPENAI_BASE_URL not set — AI parsing disabled")
		return &Client{HTTPClient: &http.Client{Timeout: 30 * time.Second}}
	}
	baseURL = strings.TrimRight(baseURL, "/")

	model := os.Getenv("OPENAI_MODEL")
	if model == "" {
		model = "local-model"
	}

	log.Printf("[ai] using %s  model=%q", baseURL, model)
	return &Client{
		BaseURL:    baseURL,
		Model:      model,
		HTTPClient: &http.Client{Timeout: 90 * time.Second},
	}
}

// ParseResult is the deserialized LLM output before domain conversion.
type ParseResult struct {
	Parsed struct {
		Intent       *string  `json:"intent"`
		DanceStyle   *string  `json:"dance_style"`
		Level        *string  `json:"level"`
		StudentCount *int     `json:"student_count"`
		RequestedTime *string `json:"requested_time"`
		NeedsPricing *bool   `json:"needs_pricing"`
		Confidence   *float64 `json:"confidence"`
	} `json:"parsed"`
	Draft string `json:"draft"`
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

	if r.Parsed.Intent != nil {
		v := *r.Parsed.Intent
		if validIntents[v] {
			lead.RequestType = &v
		} else {
			fallback := "general_question"
			lead.RequestType = &fallback
		}
	}

	if r.Parsed.DanceStyle != nil {
		v := *r.Parsed.DanceStyle
		if validStyles[v] {
			lead.DanceStyle = &v
		}
	}

	if r.Parsed.Level != nil {
		v := *r.Parsed.Level
		if validLevels[v] {
			lead.Level = &v
		}
	}

	if r.Parsed.StudentCount != nil {
		n := int32(*r.Parsed.StudentCount)
		lead.StudentCount = &n
	}

	if r.Parsed.RequestedTime != nil {
		lead.RequestedTime = r.Parsed.RequestedTime
	}

	if r.Parsed.Confidence != nil {
		c := clampConfidence(*r.Parsed.Confidence)
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
		MaxTokens:   600,
	
		Messages: []ChatMessage{
			{Role: "system", Content: SystemPrompt},
			{Role: "user", Content: UserPrompt(sender, subject, bodyPreview)},
		},
	}

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(reqBody); err != nil {
		return nil, "", fmt.Errorf("encode request: %w", err)
	}

	endpoint := c.BaseURL + "/v1/chat/completions"
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

	var chatResp ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return nil, "", fmt.Errorf("decode response: %w", err)
	}

	if chatResp.Error != nil {
		return nil, "", fmt.Errorf("LLM error: %s", chatResp.Error.Message)
	}
	if len(chatResp.Choices) == 0 {
		return nil, "", fmt.Errorf("LLM returned no choices")
	}

	raw := strings.TrimSpace(chatResp.Choices[0].Message.Content)
	// strip optional markdown code fences
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)

	var pr ParseResult
	if err := json.Unmarshal([]byte(raw), &pr); err != nil {
		return nil, "", fmt.Errorf("json unmarshal (%q): %w", raw, err)
	}

	lead := pr.ToLead()
	return lead, pr.Draft, nil
}

// StringPtr helper.
func stringPtr(s string) *string { return &s }
