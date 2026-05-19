package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"github.com/joho/godotenv"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
)

func main() {
	// Load .env if present
	_ = godotenv.Load()

	credPath := os.Getenv("GMAIL_CREDENTIALS")
	if credPath == "" {
		log.Fatal("GMAIL_CREDENTIALS must point to the OAuth2 client-secret JSON file\n" +
			"\n  How to get it:\n" +
			"  1. Go to https://console.cloud.google.com/apis/credentials\n" +
			"  2. Select your project (or create one)\n" +
			"  3. Click 'Create Credentials' → 'OAuth 2.0 Client ID'\n" +
			"  4. Application type: 'Desktop app'\n" +
			"  5. Download the JSON and save it as credentials.json in this project root\n" +
			"  6. Also enable 'Gmail API' in the project first (APIs & Services → Library)")
	}
	tokenPath := os.Getenv("GMAIL_TOKEN")
	if tokenPath == "" {
		tokenPath = "token.json"
	}
	userEmail := os.Getenv("GMAIL_USER_EMAIL")

	credBytes, err := os.ReadFile(credPath)
	if err != nil {
		log.Fatalf("read %s: %v", credPath, err)
	}

	config, err := google.ConfigFromJSON(credBytes, gmail.GmailReadonlyScope, gmail.GmailSendScope)
	if err != nil {
		log.Fatalf("google.ConfigFromJSON: %v", err)
	}

	// local callback server
	config.RedirectURL = "http://localhost:8080/oauth2callback"
	handledCh := make(chan *oauth2.Token)
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth2callback", func(w http.ResponseWriter, r *http.Request) {
		tok, terr := config.Exchange(r.Context(), r.URL.Query().Get("code"))
		if terr != nil {
			http.Error(w, "token exchange failed: "+terr.Error(), http.StatusBadRequest)
			log.Fatalf("token exchange: %v", terr)
		}
		handledCh <- tok
		w.WriteHeader(200)
		fmt.Fprintln(w, "Authorisation successful — you may close this tab.")
	})

	srv := &http.Server{Addr: ":8080", Handler: mux}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !strings.Contains(err.Error(), "closed") {
			log.Printf("callback server: %v", err)
		}
	}()
	defer srv.Shutdown(context.Background())

	authURL := config.AuthCodeURL("state-token", oauth2.AccessTypeOffline)
	fmt.Printf("Opening browser for OAuth2 consent...\n\n  %s\n\n", authURL)
	fmt.Println("Complete the consent flow and close the browser tab when done.")

	if err := openBrowser(authURL); err != nil {
		fmt.Printf("Could not open browser automatically — copy the URL above and visit it manually.\n")
	}

	tok := <-handledCh

	if userEmail != "" {
		ctx := context.Background()
		svc, terr := gmail.NewService(ctx, option.WithTokenSource(config.TokenSource(context.Background(), tok)))
		if terr != nil {
			log.Fatalf("gmail.NewService: %v", terr)
		}
		profile, terr := svc.Users.GetProfile("me").Do()
		if terr != nil {
			log.Fatalf("get profile: %v", terr)
		}
		if !strings.EqualFold(profile.EmailAddress, userEmail) {
			log.Fatalf("authorised email %q does not match GMAIL_USER_EMAIL=%q", profile.EmailAddress, userEmail)
		}
		fmt.Printf("authorised as %q\n", profile.EmailAddress)
	}

	tokenJSON, err := json.Marshal(tok)
	if err != nil {
		log.Fatalf("marshal token: %v", err)
	}
	if err := os.WriteFile(tokenPath, tokenJSON, 0600); err != nil {
		log.Fatalf("write %s: %v", tokenPath, err)
	}
	fmt.Printf("token saved to %s\n\n", tokenPath)
	fmt.Println("Set these in .env:")
	fmt.Printf("  GMAIL_CREDENTIALS=%s\n", credPath)
	fmt.Printf("  GMAIL_TOKEN=%s\n", tokenPath)
	fmt.Printf("  GMAIL_USER_EMAIL=%s\n", userEmail)
}

func openBrowser(url string) error {
	// Linux: xdg-open falls back to various DE tools
	cmd := exec.Command("xdg-open", url)
	return cmd.Start()
}
