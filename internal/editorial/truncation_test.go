package editorial

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestTruncatedProviderOutputIsNotDecodedOrCached(t *testing.T) {
	g := NewGroq("key", "model")
	calls := 0
	g.wait = func(context.Context, time.Duration) error { return nil }
	g.Client.Transport = roundTrip(func(*http.Request) (*http.Response, error) {
		calls++
		// Even valid-looking JSON must not be accepted when finish_reason is length.
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"{\"verdicts\":[]}"},"finish_reason":"length"}],"usage":{"completion_tokens":1600,"total_tokens":2000}}`))}, nil
	})
	cache := &CachedModel{Model: g, Directory: t.TempDir(), Identity: "test"}
	var out Review
	used, err := cache.Generate(context.Background(), "review", VerificationInput{Targets: []string{"story"}}, &out)
	var truncated *OutputTruncated
	if !errors.As(err, &truncated) || truncated.Limit != 3600 || used.TotalTokens != 6000 || calls != 3 {
		t.Fatalf("usage=%+v calls=%d err=%v", used, calls, err)
	}
	entries, _ := os.ReadDir(cache.Directory)
	if len(entries) != 0 {
		t.Fatal("truncated output cached")
	}
}

type truncatingReviewer struct {
	permanent     bool
	calls, splits int
}

func (m *truncatingReviewer) Generate(_ context.Context, _ string, in, out any) (Usage, error) {
	m.calls++
	b := in.(VerificationInput)
	if len(b.Targets) > 1 || m.permanent {
		m.splits++
		return Usage{TotalTokens: 2}, &OutputTruncated{Limit: 1600}
	}
	out.(*Review).Verdicts = []Verdict{{b.Targets[0], "supported", "fixture"}}
	return Usage{TotalTokens: 2}, nil
}
func TestSingleTargetReviewsAvoidCombinedOutputTruncation(t *testing.T) {
	e, input, _ := repairFixture(t)
	for _, b := range verificationBatches(input, e.Content) {
		if len(b.Targets) != 1 {
			t.Fatal("planner exceeded output workload limit")
		}
	}
	m := &truncatingReviewer{}
	cache := &CachedModel{Model: m, Directory: t.TempDir(), Identity: "test"}
	review, used, err := Verify(context.Background(), cache, input, e.Content)
	if err != nil || len(review.Verdicts) != len(e.Content.Targets()) || used.TotalTokens != m.calls*2 || m.splits != 0 {
		t.Fatalf("calls=%d usage=%+v err=%v", m.calls, used, err)
	}
}
func TestSingleTargetTruncationStopsWithoutRetryLoop(t *testing.T) {
	e, input, _ := repairFixture(t)
	m := &truncatingReviewer{permanent: true}
	_, _, err := Verify(context.Background(), m, input, e.Content)
	var truncated *OutputTruncated
	if !errors.As(err, &truncated) || m.calls > 2 {
		t.Fatalf("calls=%d err=%v", m.calls, err)
	}
}
