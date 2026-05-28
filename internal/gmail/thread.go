package gmail

import (
	"fmt"
	"strings"

	gm "google.golang.org/api/gmail/v1"

	"github.com/wmagda/tanda-lead-intake/internal/parseutil"
)

// ThreadHasStudioReplyAfter reports whether the studio sent a message in this Gmail thread
// after the given inbound message time (InternalDate ms). Used to avoid re-creating drafts
// when DB rows were deleted but Gmail already has our reply.
func ThreadHasStudioReplyAfter(svc *gm.Service, threadID, selfEmail string, afterInternalMs int64) (bool, error) {
	if svc == nil || strings.TrimSpace(threadID) == "" || afterInternalMs <= 0 {
		return false, nil
	}
	selfEmail = strings.ToLower(strings.TrimSpace(selfEmail))

	thr, err := svc.Users.Threads.Get("me", threadID).Format("full").Do()
	if err != nil {
		return false, fmt.Errorf("threads.get: %w", err)
	}

	for _, m := range thr.Messages {
		if m.InternalDate <= afterInternalMs {
			continue
		}
		if messageHasLabel(m, "SENT") {
			return true, nil
		}
		from := headerValue(m, "From")
		_, addr := parseutil.SenderFrom(from)
		if strings.EqualFold(addr, selfEmail) {
			return true, nil
		}
	}
	return false, nil
}

func messageHasLabel(msg *gm.Message, label string) bool {
	for _, id := range msg.LabelIds {
		if id == label {
			return true
		}
	}
	return false
}

func headerValue(msg *gm.Message, name string) string {
	if msg.Payload == nil {
		return ""
	}
	for _, h := range msg.Payload.Headers {
		if strings.EqualFold(h.Name, name) {
			return h.Value
		}
	}
	return ""
}
