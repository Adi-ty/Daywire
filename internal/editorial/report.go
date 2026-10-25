package editorial

import (
	"context"
	"github.com/Adi-ty/daywire/internal/content"
)

// GenerationReport is private run metadata, not publication content. It records
// deliberate omissions separately from operational errors and source outages.
type GenerationReport struct {
	Error            string         `json:"error,omitempty"`
	Status           string         `json:"status"`
	SelectedArticles int            `json:"selected_articles"`
	Retained         RetainedCounts `json:"retained"`
	Omissions        []Omission     `json:"omissions"`
}
type RetainedCounts struct {
	Articles  int `json:"articles"`
	Lessons   int `json:"lessons"`
	Questions int `json:"questions"`
	Segments  int `json:"segments"`
}
type Omission struct {
	Kind     string `json:"kind"`
	TargetID string `json:"target_id"`
	Verdict  string `json:"verdict"`
	Reason   string `json:"reason"`
}

func GenerateDraftWithReport(ctx context.Context, model Model, input Input, draft *content.Draft, logf func(string, ...any)) (Usage, GenerationReport, error) {
	report := GenerationReport{Status: "incomplete", Omissions: []Omission{}}
	usage, err := generateDraft(ctx, model, input, draft, &report, logf)
	if err != nil {
		report.Error = err.Error()
	} else {
		report.Status = "draft_complete"
		if logf != nil {
			logf("Draft complete: %d articles, %d lessons, %d questions, %d radio segments; %d omissions", report.Retained.Articles, report.Retained.Lessons, report.Retained.Questions, report.Retained.Segments, len(report.Omissions))
		}
	}
	return usage, report, err
}
