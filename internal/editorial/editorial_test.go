package editorial

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestReviewRequiresEveryTarget(t *testing.T) {
	targets := []string{"edition", "story"}
	if Accept(Review{Verdicts: []Verdict{{"edition", "supported", "yes"}}}, targets) == nil {
		t.Fatal("accepted incomplete review")
	}
	review := Review{Verdicts: []Verdict{{"edition", "supported", "yes"}, {"story", "contradicted", "wrong version"}}}
	var rejected *Rejection
	if !errors.As(Accept(review, targets), &rejected) {
		t.Fatal("expected factual rejection")
	}
	review.Verdicts[1].Verdict = "unknown"
	if errors.As(Accept(review, targets), &rejected) {
		t.Fatal("malformed review is not a factual rejection")
	}
}

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestGroqContractAndTruncation(t *testing.T) {
	for _, finish := range []string{"stop", "length"} {
		t.Run(finish, func(t *testing.T) {
			g := NewGroq("test-key", "test-model")
			g.wait = func(context.Context, time.Duration) error { return nil }
			g.Client.Transport = transport(func(r *http.Request) (*http.Response, error) {
				var request map[string]any
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Fatal(err)
				}
				schema := request["response_format"].(map[string]any)["json_schema"].(map[string]any)["schema"].(map[string]any)
				if schema["type"] != "object" {
					t.Fatal("root must be object")
				}
				if r.Header.Get("Authorization") != "Bearer test-key" {
					t.Fatal("missing key")
				}
				body, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": `{"verdicts":[]}`}, "finish_reason": finish}}})
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(body))), Header: http.Header{}}, nil
			})
			var output Review
			_, err := g.Generate(context.Background(), "system", map[string]string{"evidence": "untrusted"}, &output)
			if (err == nil) != (finish == "stop") {
				t.Fatalf("finish=%s err=%v", finish, err)
			}
		})
	}
}
