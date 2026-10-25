package editorial

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestReasoningPolicyIsModelAndTaskSpecific(t *testing.T) {
	for _, tc := range []struct {
		model  string
		output any
		effort string
	}{
		{"openai/gpt-oss-120b", &radioOutput{}, "low"},
		{"openai/gpt-oss-20b", &articleOutput{}, "low"},
		{"openai/gpt-oss-120b", &Review{}, "medium"},
		{"another-model", &radioOutput{}, ""},
	} {
		raw, _, _, err := requestBody(tc.model, "rules", struct{}{}, tc.output)
		if err != nil {
			t.Fatal(err)
		}
		var payload map[string]any
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatal(err)
		}
		effort, _ := payload["reasoning_effort"].(string)
		if effort != tc.effort {
			t.Fatalf("model=%s output=%T effort=%s", tc.model, tc.output, effort)
		}
	}
}

func TestRadioRecoveryEscalatesWithinQuotaWithoutChangingContract(t *testing.T) {
	g := NewGroq("test", "openai/gpt-oss-120b")
	g.wait = func(context.Context, time.Duration) error { return nil }
	calls := 0
	var contract string
	g.Client.Transport = roundTrip(func(req *http.Request) (*http.Response, error) {
		calls++
		raw, _ := io.ReadAll(req.Body)
		var payload map[string]any
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatal(err)
		}
		cap := int(payload["max_completion_tokens"].(float64))
		if cap != 1000+calls*1000 {
			t.Fatalf("attempt %d cap %d", calls, cap)
		}
		if (len(raw)+2)/3+256+cap > requestBudget {
			t.Fatal("recovery exceeded quota budget")
		}
		delete(payload, "max_completion_tokens")
		same, _ := json.Marshal(payload)
		if calls == 1 {
			contract = string(same)
		} else if contract != string(same) {
			t.Fatal("changed schema/prompt/evidence")
		}
		status, body := 400, `{"error":{"code":"json_validate_failed"}}`
		if calls == 2 {
			status, body = 200, `{"choices":[{"message":{"content":"{\"segment\":{}}"},"finish_reason":"length"}],"usage":{"total_tokens":3000}}`
		}
		if calls == 3 {
			status, body = 200, `{"choices":[{"message":{"content":"{\"segment\":{\"id\":\"radio\",\"title\":\"Headline\",\"text\":\"Spoken story\",\"article_ids\":[\"story\"]}}"},"finish_reason":"stop"}],"usage":{"total_tokens":1000}}`
		}
		return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	var out radioOutput
	used, err := g.Generate(context.Background(), Prompt("radio"), map[string]string{"article": "trusted story"}, &out)
	if err != nil || calls != 3 || used.TotalTokens != 4000 || out.Segment.ID != "radio" {
		t.Fatalf("calls=%d usage=%+v err=%v", calls, used, err)
	}
}

func TestRecoveryStopsWhenRequestHasNoMoreHeadroom(t *testing.T) {
	g := NewGroq("test", "openai/gpt-oss-120b")
	g.wait = func(context.Context, time.Duration) error { return nil }
	calls, previous := 0, 0
	g.Client.Transport = roundTrip(func(req *http.Request) (*http.Response, error) {
		calls++
		raw, _ := io.ReadAll(req.Body)
		var payload map[string]any
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatal(err)
		}
		cap := int(payload["max_completion_tokens"].(float64))
		if cap <= previous {
			t.Fatal("repeated unchanged request")
		}
		if (len(raw)+2)/3+256+cap > requestBudget {
			t.Fatal("request exceeded budget")
		}
		previous = cap
		return &http.Response{StatusCode: 400, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"error":{"code":"json_validate_failed"}}`))}, nil
	})
	var out Review
	_, err := g.Generate(context.Background(), "rules", strings.Repeat("x", 14000), &out)
	if err == nil || !strings.Contains(err.Error(), "headroom") || calls != 2 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}
