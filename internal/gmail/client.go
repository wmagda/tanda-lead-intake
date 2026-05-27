package gmail

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	gm "google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
)

// NewGmailService builds an authenticated Gmail API service from env vars.
// Requires GMAIL_CREDENTIALS (OAuth client JSON) and GMAIL_TOKEN (saved token).
func NewGmailService(ctx context.Context) (*gm.Service, error) {
	credPath := os.Getenv("GMAIL_CREDENTIALS")
	if credPath == "" {
		return nil, fmt.Errorf("GMAIL_CREDENTIALS not set")
	}
	tokenPath := os.Getenv("GMAIL_TOKEN")
	if tokenPath == "" {
		return nil, fmt.Errorf("GMAIL_TOKEN not set")
	}

	credBytes, err := os.ReadFile(credPath)
	if err != nil {
		return nil, fmt.Errorf("read credentials %s: %w", credPath, err)
	}

	config, err := google.ConfigFromJSON(credBytes, gm.GmailReadonlyScope, gm.GmailSendScope)
	if err != nil {
		return nil, fmt.Errorf("parse credentials: %w", err)
	}

	tokenBytes, err := os.ReadFile(tokenPath)
	if err != nil {
		return nil, fmt.Errorf("read token %s: %w", tokenPath, err)
	}

	var tok oauth2.Token
	if err := json.Unmarshal(tokenBytes, &tok); err != nil {
		return nil, fmt.Errorf("parse token: %w", err)
	}

	client := config.Client(ctx, &tok)
	svc, err := gm.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return nil, fmt.Errorf("gmail.NewService: %w", err)
	}
	return svc, nil
}
