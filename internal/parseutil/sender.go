package parseutil

import "strings"

// SenderFrom extracts a display name and email address from a "From:" header value.
//
// Input:  "John Doe <john@example.com>" or "john@example.com"
// Output: name="John Doe", email="john@example.com"
func SenderFrom(fromHeader string) (name, email string) {
	fromHeader = strings.TrimSpace(fromHeader)
	if fromHeader == "" {
		return "", ""
	}
	if i := strings.LastIndex(fromHeader, "<"); i != -1 {
		namePart := strings.TrimSpace(fromHeader[:i])
		addrPart := fromHeader[i+1:]
		if j := strings.Index(addrPart, ">"); j != -1 {
			addrPart = addrPart[:j]
		}
		return namePart, strings.ToLower(strings.TrimSpace(addrPart))
	}
	return "", strings.ToLower(strings.TrimSpace(fromHeader))
}
