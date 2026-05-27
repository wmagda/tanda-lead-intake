package ai

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestChatResponse_ErrorField_StringOrObject(t *testing.T) {
	cases := []struct {
		raw     string
		wantErr string
	}{
		{`{"error":"model not found","choices":[]}`, "model not found"},
		{`{"error":{"message":"rate limited"},"choices":[]}`, "rate limited"},
	}
	for _, tc := range cases {
		var resp ChatResponse
		if err := json.Unmarshal([]byte(tc.raw), &resp); err != nil {
			t.Fatalf("unmarshal %q: %v", tc.raw, err)
		}
		if resp.Error == nil || resp.Error.Message != tc.wantErr {
			t.Fatalf("got error %#v, want %q", resp.Error, tc.wantErr)
		}
	}
}

func TestExtractJSONObject_FromMarkdown(t *testing.T) {
	raw := "```json\n{\"intent\":\"group_class\",\"draft\":\"Hi\"}\n```"
	got, err := extractJSONObject(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got[0] != '{' {
		t.Fatalf("got %q", got)
	}
}

func TestExtractJSONObject_EmbeddedInText(t *testing.T) {
	raw := "Here is the result:\n{\"intent\":\"pricing\",\"draft\":\"x\"}\nThanks"
	got, err := extractJSONObject(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got != `{"intent":"pricing","draft":"x"}` {
		t.Fatalf("got %q", got)
	}
}

func TestExtractJSONObject_IgnoresTrailingProse(t *testing.T) {
	raw := "{\n  \"intent\": \"private_lesson\",\n  \"draft\": \"Hi\"\n}\nWarm welcome reply from the studio."
	got, err := extractJSONObject(raw)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "Warm welcome") {
		t.Fatalf("trailing prose leaked into JSON: %q", got)
	}
	if !strings.Contains(got, "private_lesson") {
		t.Fatalf("got %q", got)
	}
}

func TestNormalizeBaseURL(t *testing.T) {
	cases := map[string]string{
		"http://host:1234":     "http://host:1234/v1",
		"http://host:1234/":    "http://host:1234/v1",
		"http://host:1234/v1":  "http://host:1234/v1",
		"http://host:1234/v1/": "http://host:1234/v1",
	}
	for in, want := range cases {
		if got := normalizeBaseURL(in); got != want {
			t.Fatalf("%q => %q, want %q", in, got, want)
		}
	}
}

func TestChoiceMessageText_PrefersContent(t *testing.T) {
	c := ChatChoice{}
	c.Message.Content = `{"draft":"a"}`
	c.Message.ReasoningContent = `{"draft":"b"}`
	if got := choiceMessageText(c); got != `{"draft":"a"}` {
		t.Fatalf("got %q", got)
	}
}

func TestChoiceMessageText_FallsBackToReasoning(t *testing.T) {
	c := ChatChoice{}
	c.Message.ReasoningContent = `{"draft":"b"}`
	if got := choiceMessageText(c); got != `{"draft":"b"}` {
		t.Fatalf("got %q", got)
	}
}

func TestRequestTimeout_Default(t *testing.T) {
	os.Unsetenv("OPENAI_TIMEOUT")
	os.Unsetenv("AI_TIMEOUT")
	if got := RequestTimeout(); got != defaultRequestTimeout {
		t.Fatalf("got %v, want %v", got, defaultRequestTimeout)
	}
}

func TestIsRetryableParseError(t *testing.T) {
	cases := []struct {
		err      string
		retryable bool
	}{
		{"LLM HTTP 500: Internal Server Error", true},
		{"http do: connection reset", true},
		{"json unmarshal (\"{\"): invalid", false},
		{"LLM error: bad model", false},
	}
	for _, tc := range cases {
		got := isRetryableParseError(fmt.Errorf("%s", tc.err))
		if got != tc.retryable {
			t.Fatalf("%q: got %v want %v", tc.err, got, tc.retryable)
		}
	}
}

func TestRetryMax_Default(t *testing.T) {
	os.Unsetenv("OPENAI_RETRY_MAX")
	if RetryMax() != 3 {
		t.Fatalf("got %d", RetryMax())
	}
}

func TestRequestTimeout_FromEnv(t *testing.T) {
	os.Setenv("OPENAI_TIMEOUT", "15m")
	defer os.Unsetenv("OPENAI_TIMEOUT")
	if got := RequestTimeout(); got != 15*time.Minute {
		t.Fatalf("got %v", got)
	}
}
