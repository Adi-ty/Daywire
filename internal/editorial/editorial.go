package editorial

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"strings"

	"github.com/Adi-ty/daywire/internal/content"
)

//go:embed prompts/*.txt
var prompts embed.FS

func Prompt(name string) string {
	data, err := prompts.ReadFile("prompts/" + name + ".txt")
	if err != nil {
		panic(err)
	}
	return string(data)
}
func PromptHash() string {
	return content.Hash([]byte("newsroom-v8-newspaper-selection-and-depth" + stageRules + selectionRules + articleDepth(0) + articleDepth(1) + articleDepth(3) + Prompt("draft") + Prompt("article") + Prompt("radio") + Prompt("quiz") + Prompt("verify")))
}

// Separate review identity preserves saved drafts when review policy changes.
func ReviewHash() string {
	return content.Hash([]byte("dependency-review-v3-single-target" + Prompt("verify") + Prompt("edition-review") + Prompt("quiz-review") + Prompt("radio-review")))
}

func verificationPrompt(batch VerificationInput) string {
	if len(batch.Targets) > 0 {
		switch reviewKind(batch.Draft, batch.Targets[0]) {
		case "edition":
			return Prompt("edition-review")
		case "quiz":
			return Prompt("quiz-review")
		case "radio":
			return Prompt("radio-review")
		}
	}
	return Prompt("verify")
}

type Input struct {
	Date              string             `json:"date"`
	Evidence          []content.Evidence `json:"evidence"`
	PreviousHeadlines []string           `json:"previous_headlines"`
}
type Verdict struct {
	TargetID string `json:"target_id"`
	Verdict  string `json:"verdict"`
	Reason   string `json:"reason"`
}
type Review struct {
	Verdicts []Verdict `json:"verdicts"`
}

type Rejection struct{ Target, Verdict string }

func (r *Rejection) Error() string {
	return fmt.Sprintf("verification rejected %s: %s", r.Target, r.Verdict)
}

func Verify(ctx context.Context, model Model, input Input, draft content.Draft) (Review, Usage, error) {
	var review Review
	var usage Usage
	// Keep every cited source during verification; writer preparation already
	// limits new editions. Evaluation fixtures retain their full frozen evidence.
	batches := verificationBatches(input, draft)
	// Catch oversized later targets before spending quota on earlier batches.
	largest := 0
	for _, batch := range batches {
		_, estimate, completion, err := requestBody("openai/gpt-oss-120b", verificationPrompt(batch), batch, &Review{})
		if err != nil {
			return Review{}, usage, fmt.Errorf("review preflight targets %v: %w", batch.Targets, err)
		}
		largest = max(largest, estimate+completion)
	}
	progress(model, "Review plan: %d batches, %d targets; largest estimated request %d/%d tokens including completion", len(batches), len(draft.Targets()), largest, requestBudget)
	singleRetries := map[string]bool{}
	for index := 0; index < len(batches); index++ {
		batch := batches[index]
		progress(model, "Review batch %d/%d: %d targets", index+1, len(batches), len(batch.Targets))
		var part Review
		used, err := model.Generate(ctx, verificationPrompt(batch), batch, &part)
		usage.PromptTokens += used.PromptTokens
		usage.CompletionTokens += used.CompletionTokens
		usage.TotalTokens += used.TotalTokens
		if err == nil {
			var rejected *Rejection
			if check := Accept(part, batch.Targets); check != nil && !errors.As(check, &rejected) {
				err = check
			}
		}
		if err != nil {
			var coverage *CoverageError
			malformed := errors.As(err, &coverage)
			var oversized *RequestTooLarge
			var truncated *OutputTruncated
			if (errors.As(err, &oversized) || errors.As(err, &truncated) || malformed) && len(batch.Targets) > 1 {
				progress(model, "Review batch incomplete, truncated or oversized; splitting into smaller checks")
				middle := len(batch.Targets) / 2
				split := []VerificationInput{reviewSubset(input, draft, batch.Targets[:middle]), reviewSubset(input, draft, batch.Targets[middle:])}
				batches = append(batches[:index], append(split, batches[index+1:]...)...)
				index--
				continue
			}
			if malformed && len(batch.Targets) == 1 && !singleRetries[batch.Targets[0]] {
				singleRetries[batch.Targets[0]] = true
				progress(model, "Review response invalid (%s); retrying single target once", coverage.Reason)
				index--
				continue
			}
			return Review{}, usage, fmt.Errorf("review targets %v: %w", batch.Targets, err)
		} // Do not return an incomplete review as a final result.
		coverageErr := Accept(part, batch.Targets)
		var rejected *Rejection
		if coverageErr != nil && !errors.As(coverageErr, &rejected) {
			return Review{}, usage, coverageErr
		}
		rejectedCount := 0
		for _, v := range part.Verdicts {
			if v.Verdict != "supported" {
				rejectedCount++
				progress(model, "Review flagged %s: %s", v.TargetID, v.Verdict)
			}
		}
		progress(model, "Review batch %d/%d complete: %d supported, %d flagged", index+1, len(batches), len(part.Verdicts)-rejectedCount, rejectedCount)
		review.Verdicts = append(review.Verdicts, part.Verdicts...)
	}
	return review, usage, Accept(review, draft.Targets())
}

// CoverageError is a malformed reviewer response, not an editorial rejection.
// Keep reasons categorical: untrusted model text never enters public logs.
type CoverageError struct{ Reason string }

func (e *CoverageError) Error() string { return "invalid verification coverage: " + e.Reason }

func Accept(review Review, targets []string) error {
	wanted := map[string]bool{}
	for _, id := range targets {
		wanted[id] = true
	}
	seen := map[string]bool{}
	var rejection *Rejection
	for _, v := range review.Verdicts {
		if !wanted[v.TargetID] {
			return &CoverageError{"unexpected target ID"}
		}
		if seen[v.TargetID] {
			return &CoverageError{"duplicate target ID"}
		}
		if strings.TrimSpace(v.Reason) == "" {
			return &CoverageError{"empty reason"}
		}
		seen[v.TargetID] = true
		if v.Verdict != "supported" && v.Verdict != "contradicted" && v.Verdict != "insufficient_evidence" {
			return &CoverageError{"unknown verdict"}
		}
		if v.Verdict != "supported" && rejection == nil {
			rejection = &Rejection{Target: v.TargetID, Verdict: v.Verdict}
		}
	}
	if len(seen) != len(wanted) {
		return &CoverageError{"missing required target"}
	}
	if rejection != nil {
		return rejection
	}
	return nil
}

func progress(model Model, format string, args ...any) {
	if logger, ok := model.(interface{ Progress(string, ...any) }); ok {
		logger.Progress(format, args...)
	}
}
func (g *Groq) Progress(format string, args ...any) {
	if g.Logf != nil {
		g.Logf(format, args...)
	}
}
func (m *CachedModel) Progress(format string, args ...any) {
	if m.Logf != nil {
		m.Logf(format, args...)
	}
}
