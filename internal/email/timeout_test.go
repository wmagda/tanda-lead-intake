package email

import (
	"os"
	"testing"
	"time"
)

func TestRequestTimeout_Default(t *testing.T) {
	os.Unsetenv("OPENAI_TIMEOUT")
	os.Unsetenv("AI_TIMEOUT")
	if got := RequestTimeout(); got != defaultRequestTimeout {
		t.Fatalf("got %v, want %v", got, defaultRequestTimeout)
	}
}

func TestRequestTimeout_FromEnv(t *testing.T) {
	os.Setenv("OPENAI_TIMEOUT", "15m")
	defer os.Unsetenv("OPENAI_TIMEOUT")
	if got := RequestTimeout(); got != 15*time.Minute {
		t.Fatalf("got %v", got)
	}
}
