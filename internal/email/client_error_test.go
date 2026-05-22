package email

import (
	"encoding/json"
	"testing"
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
