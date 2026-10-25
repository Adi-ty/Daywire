// Package evaluation evaluates AI-generated editorial output against frozen
// evidence. Parser, concurrency, and UI tests live outside this package.
package evaluation

import (
	"context"
	"errors"
	"time"

	"github.com/Adi-ty/daywire/internal/content"
	"github.com/Adi-ty/daywire/internal/editorial"
)

type Case struct {
	ID             string          `json:"id"`
	Description    string          `json:"description"`
	ExpectedAccept bool            `json:"expected_accept"`
	Input          editorial.Input `json:"input"`
	Draft          content.Draft   `json:"draft"`
}
type Result struct {
	ID             string           `json:"id"`
	ExpectedAccept bool             `json:"expected_accept"`
	Accepted       bool             `json:"accepted"`
	Match          bool             `json:"match"`
	Error          string           `json:"error"`
	Review         editorial.Review `json:"review"`
	Usage          editorial.Usage  `json:"usage"`
}
type Report struct {
	Model        string    `json:"model"`
	PromptHash   string    `json:"prompt_hash"`
	CreatedAt    time.Time `json:"created_at"`
	Cases        []Result  `json:"cases"`
	FalseAccepts int       `json:"false_accepts"`
	FalseRejects int       `json:"false_rejects"`
	Errors       int       `json:"errors"`
}

type Options struct {
	CaseTimeout time.Duration
	Progress    func(index, total int, id string, result *Result)
}

func Run(ctx context.Context, model editorial.Model, name string, cases []Case) Report {
	return RunWithOptions(ctx, model, name, cases, Options{CaseTimeout: 5 * time.Minute})
}

func RunWithOptions(ctx context.Context, model editorial.Model, name string, cases []Case, options Options) Report {
	r := Report{Model: name, PromptHash: editorial.ReviewHash(), CreatedAt: time.Now().UTC(), Cases: []Result{}}
	for index, c := range cases {
		if ctx.Err() != nil {
			break
		}
		if options.Progress != nil {
			options.Progress(index+1, len(cases), c.ID, nil)
		}
		timeout := options.CaseTimeout
		if timeout <= 0 {
			timeout = 5 * time.Minute
		}
		caseCtx, cancel := context.WithTimeout(ctx, timeout)
		result := Result{ID: c.ID, ExpectedAccept: c.ExpectedAccept}
		sources := []content.Source{}
		for _, e := range c.Input.Evidence {
			sources = append(sources, e.Source)
		}
		err := content.ValidateDraft(c.Draft, sources)
		invalidOutput := err != nil
		if err == nil {
			result.Review, result.Usage, err = editorial.Verify(caseCtx, model, c.Input, c.Draft)
		}
		cancel()
		result.Accepted = err == nil
		if err != nil {
			result.Error = err.Error()
		}
		result.Match = result.Accepted == c.ExpectedAccept
		var rejection *editorial.Rejection
		operationalError := err != nil && !invalidOutput && !errors.As(err, &rejection)
		if operationalError {
			result.Match = false
			r.Errors++
		}
		if result.Accepted && !c.ExpectedAccept {
			r.FalseAccepts++
		}
		if !result.Accepted && c.ExpectedAccept && !operationalError {
			r.FalseRejects++
		}
		r.Cases = append(r.Cases, result)
		if options.Progress != nil {
			options.Progress(index+1, len(cases), c.ID, &result)
		}
		if ctx.Err() != nil {
			break
		}
	}
	return r
}
