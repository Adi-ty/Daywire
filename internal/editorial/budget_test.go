package editorial

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestOversizeRejectedBeforeNetwork(t *testing.T) {
	g := NewGroq("test", "openai/gpt-oss-120b")
	g.Client.Transport = roundTrip(func(*http.Request) (*http.Response, error) {
		t.Fatal("oversize request reached network")
		return nil, nil
	})
	var review Review
	_, err := g.Generate(context.Background(), "system", strings.Repeat("x", 30000), &review)
	var oversized *RequestTooLarge
	if !errors.As(err, &oversized) {
		t.Fatalf("expected typed budget error: %v", err)
	}
}
func TestPacingUsesSuccessfulResponseHeaders(t *testing.T) {
	now := time.Now()
	g := NewGroq("test", "test")
	g.now = func() time.Time { return now }
	calls := 0
	g.Client.Transport = roundTrip(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Header: http.Header{"X-Ratelimit-Remaining-Tokens": []string{"100"}, "X-Ratelimit-Reset-Tokens": []string{"22s"}}, Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"{\"verdicts\":[]}"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":10,"total_tokens":20}}`))}, nil
	})
	waits := []time.Duration{}
	g.wait = func(_ context.Context, d time.Duration) error { waits = append(waits, d); now = now.Add(d); return nil }
	for i := 0; i < 2; i++ {
		var review Review
		if _, err := g.Generate(context.Background(), "system", struct{}{}, &review); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 2 || len(waits) != 1 || waits[0] != 23*time.Second {
		t.Fatalf("calls=%d waits=%v", calls, waits)
	}
}
func TestPacingCancellationAndDeadline(t *testing.T) {
	g := NewGroq("test", "test")
	g.resetAt = time.Now().Add(time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if !errors.Is(g.pace(ctx, 100), context.Canceled) {
		t.Fatal("pacing ignored cancellation")
	}
	ctx, cancel = context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if err := g.pace(ctx, 100); err == nil {
		t.Fatal("pacing exceeded deadline")
	}
}
func TestPacingMissingHeadersAndDailyExhaustion(t *testing.T) {
	now := time.Now()
	g := NewGroq("test", "test")
	g.now = func() time.Time { return now }
	g.observe(http.Header{})
	if g.resetAt.Sub(now) != 61*time.Second {
		t.Fatal("missing headers caused burst")
	}
	g.observe(http.Header{"X-Ratelimit-Remaining-Requests": []string{"0"}, "X-Ratelimit-Reset-Requests": []string{"24h"}, "X-Ratelimit-Remaining-Tokens": []string{"8000"}, "X-Ratelimit-Reset-Tokens": []string{"1s"}})
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := g.pace(ctx, 100); err == nil {
		t.Fatal("exhausted daily quota must stop within deadline")
	}
}

func TestWaitingForAnotherModelCallIsCancellable(t *testing.T) {
	g := NewGroq("test", "test")
	g.gate <- struct{}{}
	defer func() { <-g.gate }()
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	var review Review
	if _, err := g.Generate(ctx, "system", struct{}{}, &review); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}
