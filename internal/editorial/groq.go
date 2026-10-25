package editorial

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Adi-ty/daywire/internal/content"
)

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// OutputTruncated is separate from refusal. Recovery can increase the allowance
// or split the task, but must never accept a partial response.
type OutputTruncated struct{ Limit int }

func (e *OutputTruncated) Error() string {
	return fmt.Sprintf("Groq output truncated (finish_reason=length; completion cap=%d); reduce this stage's workload", e.Limit)
}

type Model interface {
	Generate(context.Context, string, any, any) (Usage, error)
}
type Groq struct {
	APIKey         string
	Model          string
	Client         *http.Client
	wait           func(context.Context, time.Duration) error
	Logf           func(string, ...any)
	DiagnosticsDir string
	gate           chan struct{}
	remaining      int
	resetAt        time.Time
	nextRequest    time.Time
	now            func() time.Time
}

func NewGroq(key, model string) *Groq {
	return &Groq{APIKey: key, Model: model, gate: make(chan struct{}, 1), Client: &http.Client{Timeout: 120 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("provider redirects are disabled") }}}
}

// Generate gives incomplete/failed structured output more completion space on
// each retry, bounded by both attempts and the request's quota budget. It never
// weakens the schema, salvages partial text, or repeats an unchanged request.
func (g *Groq) Generate(ctx context.Context, system string, input any, output any) (Usage, error) {
	var total Usage
	completion := completionBudget(output)
	for attempt := 1; attempt <= 3; attempt++ {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		used, err := g.generateOnce(ctx, system, input, output, completion)
		total.PromptTokens += used.PromptTokens
		total.CompletionTokens += used.CompletionTokens
		total.TotalTokens += used.TotalTokens
		var provider *ProviderError
		var truncated *OutputTruncated
		retryable := errors.As(err, &truncated) || (errors.As(err, &provider) && provider.Status == http.StatusBadRequest &&
			(provider.Code == "json_validate_failed" || provider.Code == "structured_generation_failed"))
		if !retryable {
			return total, err
		}
		if attempt == 3 {
			return total, fmt.Errorf("structured generation failed after %d attempts; completed stages are saved; rerun to resume: %w", attempt, err)
		}
		_, estimate, _, planErr := requestBodyWithLimit(g.Model, system, input, output, completion)
		if planErr != nil {
			return total, planErr
		}
		// Leave room for token-estimation rounding if the serialized cap grows.
		next := min(completion+1000, requestBudget-estimate-16)
		if next <= completion {
			return total, fmt.Errorf("structured generation exhausted available completion headroom; completed stages are saved: %w", err)
		}
		if g.Logf != nil {
			g.Logf("Groq structured output failed or was truncated; increasing completion allowance %d -> %d, attempt %d/3 after quota pacing", completion, next, attempt+1)
		}
		completion = next
	}
	panic("unreachable")
}

func (g *Groq) generateOnce(ctx context.Context, system string, input any, output any, completion int) (Usage, error) {
	var usage Usage
	if g.APIKey == "" || g.Model == "" {
		return usage, errors.New("GROQ_API_KEY and model are required")
	}
	user, err := json.Marshal(input)
	if err != nil {
		return usage, err
	}
	if len(user) > 180000 {
		return usage, errors.New("model input exceeds 180 KB budget")
	}
	body, estimate, completionLimit, err := requestBodyWithLimit(g.Model, system, input, output, completion)
	if err != nil {
		return usage, err
	}
	// Serialize model calls and quota state. Collection can still run concurrently.
	select {
	case g.gate <- struct{}{}:
		defer func() { <-g.gate }()
	case <-ctx.Done():
		return usage, ctx.Err()
	}
	if err := g.pace(ctx, estimate+completionLimit); err != nil {
		return usage, err
	}
	if g.Logf != nil {
		g.Logf("Request size: %d bytes; estimated input: %d tokens; completion cap: %d; budget: %d", len(body), estimate, completionLimit, requestBudget)
	}
	resp, err := g.request(ctx, body)
	if err != nil {
		return usage, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusRequestEntityTooLarge {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		var detail struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &detail)
		limits := ""
		// Only numerical limit details are safe to surface; never print provider
		// messages that could echo input text, account identifiers, or credentials.
		for _, match := range regexp.MustCompile(`(?i)(limit|requested)[: ]+([0-9]+)`).FindAllStringSubmatch(detail.Error.Message, -1) {
			limits += "; " + strings.ToLower(match[1]) + "=" + match[2]
		}
		return usage, &RequestTooLarge{Detail: fmt.Sprintf("Groq HTTP 413: request too large (%d bytes%s); reduce evidence/request size or check this model's limits at console.groq.com/settings/limits. Retrying the same request will not help", len(body), limits)}
	}
	if resp.StatusCode != http.StatusOK {
		return usage, g.responseError(resp, body, estimate, completionLimit)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
	if err != nil {
		return usage, err
	}
	if len(raw) > 2<<20 {
		return usage, errors.New("Groq response exceeds size limit")
	}
	var result struct {
		Choices []struct {
			Message struct {
				Content string  `json:"content"`
				Refusal *string `json:"refusal"`
			} `json:"message"`
			Finish string `json:"finish_reason"`
		} `json:"choices"`
		Usage Usage `json:"usage"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return usage, err
	}
	usage = result.Usage
	if g.Logf != nil {
		g.Logf("Groq usage: input=%d completion=%d total=%d", usage.PromptTokens, usage.CompletionTokens, usage.TotalTokens)
	}
	if len(result.Choices) != 1 {
		return usage, errors.New("Groq response must contain exactly one choice")
	}
	if result.Choices[0].Message.Refusal != nil {
		return usage, errors.New("Groq refused output")
	}
	if result.Choices[0].Finish == "length" {
		return usage, &OutputTruncated{Limit: completionLimit}
	}
	if result.Choices[0].Finish != "stop" {
		return usage, errors.New("Groq response ended without a completed answer")
	}
	if err := content.Decode(bytes.NewBufferString(result.Choices[0].Message.Content), output); err != nil {
		return usage, fmt.Errorf("invalid model JSON: %w", err)
	}
	return usage, nil
}

// Transport-level retries handle explicit 429s; Generate separately handles
// explicit structured-generation errors. Network errors and 5xx responses can have
// ambiguous billing outcomes and are returned without repeating the request.
func (g *Groq) request(ctx context.Context, body []byte) (*http.Response, error) {
	var waited time.Duration
	for attempt := 0; attempt < 4; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.groq.com/openai/v1/chat/completions", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+g.APIKey)
		req.Header.Set("Content-Type", "application/json")
		if g.Logf != nil {
			g.Logf("Groq request %d/4", attempt+1)
		}
		resp, err := g.Client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("Groq request: %w", err)
		}
		g.observe(resp.Header)
		if resp.StatusCode != http.StatusTooManyRequests {
			return resp, nil
		}
		delay := retryDelay(resp.Header, time.Now())
		resp.Body.Close()
		if attempt == 3 || waited+delay > 3*time.Minute {
			return nil, fmt.Errorf("Groq rate limit (HTTP 429); retry after %s or check your account's request/token limits", delay.Round(time.Second))
		}
		if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) <= delay {
			return nil, fmt.Errorf("Groq rate limit (HTTP 429): retry in %s exceeds remaining case time; retry later or increase --case-timeout", delay.Round(time.Second))
		}
		if g.Logf != nil {
			g.Logf("Groq rate limited (HTTP 429); waiting %s before retry %d/4", delay, attempt+2)
		}
		waited += delay
		wait := g.wait
		if wait == nil {
			wait = waitForRetry
		}
		if err := wait(ctx, delay); err != nil {
			return nil, err
		}
	}
	return nil, errors.New("Groq retry budget exhausted")
}

func retryDelay(h http.Header, now time.Time) time.Duration {
	if raw := strings.TrimSpace(h.Get("Retry-After")); raw != "" {
		if seconds, err := strconv.ParseFloat(raw, 64); err == nil && seconds >= 0 && seconds < 86400*7 {
			return max(time.Second, time.Duration(seconds*float64(time.Second)))
		}
		if at, err := http.ParseTime(raw); err == nil {
			return max(time.Second, at.Sub(now))
		}
	}
	if h.Get("X-Ratelimit-Remaining-Requests") == "0" {
		if d, err := time.ParseDuration(h.Get("X-Ratelimit-Reset-Requests")); err == nil && d > 0 {
			return d + time.Second
		}
	}
	if d, err := time.ParseDuration(h.Get("X-Ratelimit-Reset-Tokens")); err == nil && d > 0 {
		return d + time.Second
	}
	return time.Minute
}

func waitForRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func dereferenceSchema(value any) map[string]any {
	s := content.Schema(value)
	if unions, ok := s["anyOf"].([]any); ok {
		return unions[0].(map[string]any)
	}
	return s
}
