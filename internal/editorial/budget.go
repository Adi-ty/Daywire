package editorial

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// A safety margin below the free GPT-OSS 8K TPM limit. This is a heuristic,
// not the provider's tokenizer: include wire/schema overhead and reserve output.
// Provider 413 responses remain authoritative and trigger bounded stage splitting.
const requestBudget = 7000

type RequestTooLarge struct{ Detail string }

func (e *RequestTooLarge) Error() string { return e.Detail }

func completionBudget(output any) int {
	switch output.(type) {
	case *selectionOutput:
		return 1000
	case *Review:
		return 1600
	case *articleOutput:
		return 1800
	case *lessonOutput:
		return 1600
	case *questionOutput:
		return 1200
	case *quizOutput:
		return 1600
	case *radioOutput:
		return 1200
	case *headingOutput:
		return 1200
	default:
		return 4000
	}
}

func requestBody(model, system string, input, output any) ([]byte, int, int, error) {
	user, err := json.Marshal(input)
	if err != nil {
		return nil, 0, 0, err
	}
	completion := completionBudget(output)
	payload := map[string]any{"model": model, "temperature": 0.2, "max_completion_tokens": completion,
		"messages":        []map[string]string{{"role": "system", "content": system}, {"role": "user", "content": string(user)}},
		"response_format": map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": "daywire_output", "strict": true, "schema": outputSchema(input, output)}}}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, 0, 0, err
	}
	// Roughly three wire bytes per token plus chat overhead deliberately errs high
	// for English reporting. It does not guarantee a bound for every language.
	estimate := (len(body)+2)/3 + 256
	if estimate+completion > requestBudget {
		return body, estimate, completion, &RequestTooLarge{Detail: fmt.Sprintf("request exceeds local token budget: estimated input=%d + completion=%d > %d; split this stage", estimate, completion, requestBudget)}
	}
	return body, estimate, completion, nil
}

func (g *Groq) clock() time.Time {
	if g.now != nil {
		return g.now()
	}
	return time.Now()
}
func (g *Groq) pause(ctx context.Context, delay time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) <= delay {
		return fmt.Errorf("Groq quota wait of %s exceeds remaining run/case time", delay.Round(time.Second))
	}
	wait := g.wait
	if wait == nil {
		wait = waitForRetry
	}
	return wait(ctx, delay)
}
func (g *Groq) pace(ctx context.Context, tokens int) error {
	now := g.clock()
	until := g.nextRequest
	if now.Before(g.resetAt) && g.remaining < tokens && g.resetAt.After(until) {
		until = g.resetAt
	}
	if until.After(now) {
		if g.Logf != nil {
			g.Logf("Groq quota pacing: waiting %s before next request", until.Sub(now).Round(time.Second))
		}
		if err := g.pause(ctx, until.Sub(now)); err != nil {
			return err
		}
	}
	return ctx.Err()
}
func (g *Groq) observe(h http.Header) {
	now := g.clock()
	// 30 RPM default; token reset/remaining values usually impose a longer wait.
	g.nextRequest = now.Add(2100 * time.Millisecond)
	remaining, err := strconv.Atoi(h.Get("X-Ratelimit-Remaining-Tokens"))
	reset, resetErr := time.ParseDuration(h.Get("X-Ratelimit-Reset-Tokens"))
	if err == nil && remaining >= 0 && resetErr == nil && reset >= 0 {
		g.remaining = remaining
		g.resetAt = now.Add(reset + time.Second)
	} else {
		// Missing headers must not turn a sequential job into a burst of 429s.
		g.remaining = 0
		g.resetAt = now.Add(61 * time.Second)
	}
	// Groq's request headers refer to daily quota, not RPM.
	if h.Get("X-Ratelimit-Remaining-Requests") == "0" {
		if reset, err := time.ParseDuration(h.Get("X-Ratelimit-Reset-Requests")); err == nil && now.Add(reset+time.Second).After(g.resetAt) {
			g.remaining = 0
			g.resetAt = now.Add(reset + time.Second)
		}
	}
}

// Restrict review identifiers at decoding time, not only in prompt prose.
// Accept still enforces uniqueness and exhaustive coverage after decoding.
func outputSchema(input, output any) map[string]any {
	schema := dereferenceSchema(output)
	batch, ok := input.(VerificationInput)
	if _, review := output.(*Review); !ok || !review || len(batch.Targets) == 0 {
		return schema
	}
	props := schema["properties"].(map[string]any)
	verdicts := props["verdicts"].(map[string]any)
	fields := verdicts["items"].(map[string]any)["properties"].(map[string]any)
	fields["target_id"].(map[string]any)["enum"] = batch.Targets
	fields["verdict"].(map[string]any)["enum"] = []string{"supported", "contradicted", "insufficient_evidence"}
	return schema
}
