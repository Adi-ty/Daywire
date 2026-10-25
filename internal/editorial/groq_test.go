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

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestRetryAfter(t *testing.T) {
	now := time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		header http.Header
		want   time.Duration
	}{
		{http.Header{"Retry-After": []string{"2.5"}}, 2500 * time.Millisecond},
		{http.Header{"Retry-After": []string{now.Add(30 * time.Second).Format(http.TimeFormat)}}, 30 * time.Second},
		{http.Header{"X-Ratelimit-Reset-Tokens": []string{"12s"}}, 13 * time.Second},
		{http.Header{"Retry-After": []string{"invalid"}}, time.Minute},
	} {
		if got := retryDelay(tc.header, now); got != tc.want {
			t.Fatalf("got %s want %s", got, tc.want)
		}
	}
}
func Test429RetryIsBounded(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		code                    int
		retry                   string
		successAt, calls, waits int
		wantErr                 bool
	}{
		{"recovery", 429, "2", 2, 2, 1, false},
		{"attempt limit", 429, "1", 0, 4, 3, true},
		{"daily limit", 429, "3600", 0, 1, 0, true},
		{"no retry on server error", 500, "1", 0, 1, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls, waits := 0, 0
			g := NewGroq("test", "test")
			g.Client = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
				calls++
				code := tc.code
				if calls == tc.successAt {
					code = 200
				}
				return &http.Response{StatusCode: code, Header: http.Header{"Retry-After": []string{tc.retry}}, Body: io.NopCloser(strings.NewReader(""))}, nil
			})}
			g.wait = func(context.Context, time.Duration) error { waits++; return nil }
			response, err := g.request(context.Background(), []byte("{}"))
			if response != nil {
				response.Body.Close()
			}
			if (err != nil) != tc.wantErr || calls != tc.calls || waits != tc.waits {
				t.Fatalf("calls=%d waits=%d err=%v", calls, waits, err)
			}
		})
	}
}
func TestRequestDoesNotRetryAmbiguousNetworkFailure(t *testing.T) {
	g := NewGroq("test", "test")
	calls := 0
	g.Client = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) { calls++; return nil, errors.New("connection lost") })}
	if _, err := g.request(context.Background(), nil); err == nil || calls != 1 {
		t.Fatal("network failure retried or ignored")
	}
}
func TestRetryWaitCancels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitForRetry(ctx, time.Minute); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func Test429RespectsCaseBudget(t *testing.T) {
	g := NewGroq("test", "test")
	g.Client = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": []string{"60"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	g.wait = func(context.Context, time.Duration) error { t.Fatal("wait exceeds remaining case budget"); return nil }
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := g.request(ctx, nil); err == nil || !strings.Contains(err.Error(), "remaining case time") {
		t.Fatal(err)
	}
}

func Test413ShowsLimitsWithoutEchoingAccountOrInput(t *testing.T) {
	g := NewGroq("test-key", "test-model")
	calls := 0
	g.Client = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 413, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"Request too large for organization secret-account. Limit 8000, Requested 19300. private input text"}}`))}, nil
	})}
	var review Review
	_, err := g.Generate(context.Background(), "system", map[string]string{"text": "test"}, &review)
	if err == nil || !strings.Contains(err.Error(), "limit=8000") || !strings.Contains(err.Error(), "requested=19300") || strings.Contains(err.Error(), "secret-account") || strings.Contains(err.Error(), "private input") || calls != 1 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}
