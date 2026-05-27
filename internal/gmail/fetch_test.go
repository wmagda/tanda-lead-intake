package gmail

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestBuildQuery_UsesUnixAfter(t *testing.T) {
	since := time.Date(2026, 5, 24, 22, 25, 37, 0, time.UTC)
	q := buildQuery("[STUDIO-EMAIL]", since)
	want := fmt.Sprintf("after:%d", since.Unix())
	if !strings.Contains(q, want) {
		t.Fatalf("query=%q want substring %q", q, want)
	}
	if strings.Contains(q, "after:2026/05/24") {
		t.Fatalf("query should not use date-only after: %q", q)
	}
}
