package email

import (
	"strings"
	"testing"
)

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
		"http://host:1234":      "http://host:1234/v1",
		"http://host:1234/":     "http://host:1234/v1",
		"http://host:1234/v1":   "http://host:1234/v1",
		"http://host:1234/v1/":  "http://host:1234/v1",
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
