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

// Client wraps an LM Studio / compatible OpenAI-endpoint server.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewAIClientFromEnv reads env vars and returns a configured Client.
// Required: AI_BASE_URL (e.g. http://localhost:1234/v1) or LMSTUDIO_BASE_URL.
// Optional: AI_API_KEY.
func NewAIClientFromEnv() *Client {
	baseURL := os.Getenv("AI_BASE_URL")
	if baseURL == "" {
		baseURL = os.Getenv("LMSTUDIO_BASE_URL")
	}
	if baseURL == "" {
		log.Println("[ai] AI_BASE_URL not set — AI parsing disabled")
		return &Client{httpClient: &http.Client{Timeout: 30 * time.Second}}
	}
	if !strings.HasSuffix(baseURL, "/v1") {
		baseURL = strings.TrimRight(baseURL, "/") + "/v1"
	}
	apiKey := os.Getenv("AI_API_KEY")
	if apiKey == "" {
		apiKey = "no-key" // local servers often allow unauthenticated access
	}
	log.Printf("[ai] using %s", baseURL)
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{Timeout: 60 * time.Second},
	}
}

// ParseExtracted parses an email body and returns structured lead info + draft.
// Expected LLM output: { "parsed": {...}, "draft": "..." }
func (c *Client) ParseExtracted(ctx context.Context, sender, subject, body string) (*models.Lead, string, error) {
	// TODO: wire to LM Studio chat completions endpoint.
	return nil, "", fmt.Errorf("not implemented — stub")
}
