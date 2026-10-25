package editorial

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestProvider400KeepsUsefulPrivateDiagnosticWithoutLoggingInputs(t *testing.T) {
	g := NewGroq("private-api-key", "test-model")
	g.DiagnosticsDir = t.TempDir()
	g.wait = func(context.Context, time.Duration) error { return nil }
	calls := 0
	g.Client.Transport = roundTrip(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 400, Header: http.Header{"X-Request-Id": []string{"req-test"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"code":"json_validate_failed","type":"invalid_request_error","param":"response_format","message":"Provider explanation with private-api-key and private source text","failed_generation":"partial generated text"}}`))}, nil
	})
	var review Review
	_, err := g.Generate(context.Background(), "system", map[string]string{"text": "input must not be saved"}, &review)
	var provider *ProviderError
	if !errors.As(err, &provider) || provider.Status != 400 || provider.Code != "json_validate_failed" || calls != 3 {
		t.Fatalf("missing classification or unexpected retry: calls=%d err=%v", calls, err)
	}
	for _, secret := range []string{"private-api-key", "private source text", "partial generated text"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatal("provider data entered public error")
		}
	}
	raw, readErr := os.ReadFile(provider.DiagnosticPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	for _, omitted := range []string{"private-api-key", "partial generated text", "input must not be saved"} {
		if strings.Contains(string(raw), omitted) {
			t.Fatal("diagnostic stored credential, request body, or failed output")
		}
	}
	var diagnostic map[string]any
	if err := json.Unmarshal(raw, &diagnostic); err != nil {
		t.Fatal(err)
	}
	if diagnostic["request_id"] != "req-test" || diagnostic["message"] != "Provider explanation with [REDACTED] and private source text" || diagnostic["failed_generation_valid_json"] != false || diagnostic["completion_cap"] != float64(3600) {
		t.Fatal("lost diagnostic information")
	}
	info, err := os.Stat(provider.DiagnosticPath)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("diagnostic permissions must be private")
	}
}

func TestUnclassifiedAndMalformedProviderErrorsStayFailures(t *testing.T) {
	for _, body := range []string{
		`{"error":{"code":"secret-code","type":"secret-type","param":"secret-param","message":"private message"}}`,
		`<html>private proxy response</html>`,
		strings.Repeat("x", 65<<10),
	} {
		g := NewGroq("test-key", "test-model")
		g.Client.Transport = roundTrip(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 400, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
		})
		var review Review
		_, err := g.Generate(context.Background(), "system", struct{}{}, &review)
		var provider *ProviderError
		if !errors.As(err, &provider) || err.Error() != "Groq HTTP 400" || len(review.Verdicts) != 0 {
			t.Fatalf("untrusted or malformed error accepted/leaked: %v", err)
		}
	}
}

func TestStructuredGenerationRecoversWithLargerAllowanceAndSameContract(t *testing.T) {
	for _, code := range []string{"json_validate_failed", "structured_generation_failed"} {
		t.Run(code, func(t *testing.T) {
			g := NewGroq("key", "model")
			waits, calls := 0, 0
			g.wait = func(context.Context, time.Duration) error { waits++; return nil }
			var first map[string]any
			g.Client.Transport = roundTrip(func(req *http.Request) (*http.Response, error) {
				calls++
				raw, _ := io.ReadAll(req.Body)
				var payload map[string]any
				if err := json.Unmarshal(raw, &payload); err != nil {
					t.Fatal(err)
				}
				if payload["max_completion_tokens"] != float64(1600+(calls-1)*1000) {
					t.Fatal("allowance did not increase")
				}
				delete(payload, "max_completion_tokens")
				if calls == 1 {
					first = payload
				} else {
					a, _ := json.Marshal(first)
					b, _ := json.Marshal(payload)
					if string(a) != string(b) {
						t.Fatal("retry changed evidence/schema")
					}
				}
				body := `{"choices":[{"message":{"content":"{\"verdicts\":[]}"},"finish_reason":"stop"}],"usage":{"total_tokens":10}}`
				status := 200
				if calls == 1 {
					status = 400
					body = `{"error":{"code":"` + code + `","failed_generation":"untrusted broken output"}}`
				}
				return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
			})
			var result Review
			used, err := g.Generate(context.Background(), "rules", struct{}{}, &result)
			if err != nil || calls != 2 || waits != 1 || used.TotalTokens != 10 {
				t.Fatalf("calls=%d waits=%d usage=%+v err=%v", calls, waits, used, err)
			}
		})
	}
}

func TestStructuredRetryHonorsCancellationDuringPacing(t *testing.T) {
	g := NewGroq("key", "model")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	g.wait = func(context.Context, time.Duration) error { cancel(); return ctx.Err() }
	g.Client.Transport = roundTrip(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 400, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"error":{"code":"json_validate_failed"}}`))}, nil
	})
	var result Review
	_, err := g.Generate(ctx, "rules", struct{}{}, &result)
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}

func TestConfigurationFailuresAreNotRetried(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 500} {
		g := NewGroq("key", "model")
		calls := 0
		g.Client.Transport = roundTrip(func(*http.Request) (*http.Response, error) {
			calls++
			return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"error":{"code":"invalid_request_error","param":"response_format"}}`))}, nil
		})
		var result Review
		if _, err := g.Generate(context.Background(), "rules", struct{}{}, &result); err == nil || calls != 1 {
			t.Fatalf("status=%d calls=%d err=%v", status, calls, err)
		}
	}
}
