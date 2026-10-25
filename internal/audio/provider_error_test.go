package audio

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestSpeechErrorsDistinguishCreditsAndVoiceEligibility(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{`{"detail":{"status":"payment_required","message":"Free users cannot use library voices via the API. private-key"}}`, "Voice Library voice requires a paid plan"},
		{`{"detail":{"status":"quota_exceeded","message":"private-key"}}`, "insufficient credits or quota"},
		{`{"error":{"code":"insufficient_credits","message":"private-key"}}`, "insufficient credits or quota"},
		{`{"detail":{"status":"payment_required","message":"private-key"}}`, "payment or a subscription is required"},
		{`{"detail":{"status":"private-key","message":"private-key"}}`, "unclassified provider error"},
		{`<html>private-key</html>`, "unrecognized error response"},
	} {
		err := speechError(&http.Response{StatusCode: 402, Body: io.NopCloser(strings.NewReader(tc.body))})
		if !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "private-key") {
			t.Fatalf("incorrect or unsafe classification: %v", err)
		}
	}
}
