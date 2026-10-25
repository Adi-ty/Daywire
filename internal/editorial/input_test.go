package editorial

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Adi-ty/daywire/internal/content"
	"github.com/Adi-ty/daywire/internal/store"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestPrepareInputIsBoundedAndPreservesSnapshot(t *testing.T) {
	input := Input{Date: "2026-10-23", Evidence: []content.Evidence{}, PreviousHeadlines: []string{}}
	for i := 0; i < 12; i++ {
		input.Evidence = append(input.Evidence, content.Evidence{Text: strings.Repeat("Evidence café. ", 1000), Kind: "article_excerpt", Hash: "original"})
	}
	result := PrepareInput(input)
	if len(result.Evidence) != 6 || input.Evidence[0].Hash != "original" {
		t.Fatal("working set mutated original")
	}
	for _, e := range result.Evidence {
		if !utf8.ValidString(e.Text) || len([]rune(e.Text)) > 1600 || !strings.HasSuffix(e.Kind, "_truncated") || content.Hash([]byte(e.Text)) != e.Hash {
			t.Fatal("invalid shortened evidence")
		}
	}
}

type batchModel struct {
	calls   int
	failAt  int
	targets []string
}

func (m *batchModel) Generate(_ context.Context, _ string, value any, out any) (Usage, error) {
	m.calls++
	if m.calls == m.failAt {
		return Usage{}, errors.New("temporary provider error")
	}
	input := value.(VerificationInput)
	review := out.(*Review)
	for _, id := range input.Targets {
		review.Verdicts = append(review.Verdicts, Verdict{id, "supported", "grounded"})
		m.targets = append(m.targets, id)
	}
	return Usage{TotalTokens: 10}, nil
}
func TestLargeReviewBatchesKeepEveryTarget(t *testing.T) {
	var e content.Edition
	if err := store.Read("../../testdata/demo/2026-10-23.json", &e); err != nil {
		t.Fatal(err)
	}
	input := Input{Date: e.Date, Evidence: []content.Evidence{}}
	for _, source := range e.Sources {
		input.Evidence = append(input.Evidence, content.Evidence{Source: source, Text: strings.Repeat("source ", 400)})
	}
	model := &batchModel{}
	review, usage, err := Verify(context.Background(), model, input, e.Content)
	if err != nil || model.calls < 2 || len(review.Verdicts) != len(e.Content.Targets()) || usage.TotalTokens != model.calls*10 {
		t.Fatalf("calls=%d usage=%+v err=%v", model.calls, usage, err)
	}
	if err := Accept(review, e.Content.Targets()); err != nil {
		t.Fatal(err)
	}
	model = &batchModel{failAt: 2}
	review, _, err = Verify(context.Background(), model, input, e.Content)
	if err == nil || len(review.Verdicts) != 0 {
		t.Fatal("partial provider result could be cached as final review")
	}
}
func TestDraftRequestBudgetAfterPreparation(t *testing.T) {
	input := Input{Date: "2026-10-23", Evidence: []content.Evidence{}}
	for i := 0; i < 12; i++ {
		input.Evidence = append(input.Evidence, content.Evidence{Source: content.Source{ID: "source-example", Name: "Publisher", Title: "A technical article", URL: "https://example.com/article"}, Text: strings.Repeat("Evidence sentence. ", 1000)})
	}
	raw, _ := json.Marshal(PrepareInput(input))
	if len(raw) > 14000 {
		t.Fatalf("prepared input grew to %d bytes", len(raw))
	}
}
