package editorial

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/Adi-ty/daywire/internal/content"
)

// Exercise the same serialization/schema/reservation as the real transport.
// These responses test orchestration only, never AI factual accuracy.
type budgetReviewer struct {
	calls  int
	reject string
}

func (m *budgetReviewer) Generate(_ context.Context, prompt string, in, out any) (Usage, error) {
	m.calls++
	if _, _, _, err := requestBody("openai/gpt-oss-120b", prompt, in, out); err != nil {
		return Usage{}, err
	}
	for _, id := range in.(VerificationInput).Targets {
		verdict := "supported"
		if id == m.reject {
			verdict = "contradicted"
		}
		out.(*Review).Verdicts = append(out.(*Review).Verdicts, Verdict{id, verdict, "synthetic test judgment"})
	}
	return Usage{}, nil
}

func TestReviewPlanUsesDirectPremisesAndPreservesSourceChecks(t *testing.T) {
	e, input, _ := repairFixture(t)
	for i := range input.Evidence {
		input.Evidence[i].Text = strings.Repeat("Evidence sentence. ", 85)
	}
	for i := 0; i < 30; i++ {
		input.PreviousHeadlines = append(input.PreviousHeadlines, strings.Repeat("Past headline ", 14))
	}
	seen := map[string]int{}
	for _, b := range verificationBatches(input, e.Content) {
		kind := reviewKind(e.Content, b.Targets[0])
		for _, id := range b.Targets {
			seen[id]++
			if reviewKind(e.Content, id) != kind {
				t.Fatal("mixed review responsibilities in one batch")
			}
		}
		switch kind {
		case "source":
			if len(b.Input.Evidence) == 0 {
				t.Fatal("source check lost evidence")
			}
			for _, ev := range b.Input.Evidence {
				found := false
				for _, original := range input.Evidence {
					if reflect.DeepEqual(ev, original) {
						found = true
					}
				}
				if !found {
					t.Fatal("source evidence was rewritten or shortened")
				}
			}
		case "quiz":
			if len(b.Input.Evidence) != 0 || len(b.Draft.Articles) != 0 || len(b.Draft.Lessons) == 0 {
				t.Fatal("quiz should depend directly on lessons")
			}
		case "radio":
			if len(b.Input.Evidence) != 0 || len(b.Draft.Articles) == 0 {
				t.Fatal("radio should depend directly on articles")
			}
			for _, a := range b.Draft.Articles {
				if len(a.Paragraphs) == 0 {
					t.Fatal("radio lost article details")
				}
			}
		case "edition":
			if len(b.Input.Evidence) != 0 || len(b.Draft.Articles) != len(e.Content.Articles) || !reflect.DeepEqual(b.Input.PreviousHeadlines, input.PreviousHeadlines) {
				t.Fatal("edition composition needs all summaries and novelty context")
			}
		}
	}
	for _, id := range e.Content.Targets() {
		if seen[id] != 1 {
			t.Fatalf("target %s reviewed %d times", id, seen[id])
		}
	}
	if _, _, err := Verify(context.Background(), &budgetReviewer{}, input, e.Content); err != nil {
		t.Fatal(err)
	}
}

func TestPassingDerivedReviewsCannotOverrideRejectedPremise(t *testing.T) {
	e, input, _ := repairFixture(t)
	for _, id := range []string{e.Content.Articles[0].ID, e.Content.Lessons[0].ID} {
		m := &budgetReviewer{reject: id}
		review, _, err := Verify(context.Background(), m, input, e.Content)
		var rejected *Rejection
		if !errors.As(err, &rejected) || rejected.Target != id || len(review.Verdicts) != len(e.Content.Targets()) {
			t.Fatalf("rejected premise was lost: %v", err)
		}
		if Accept(review, e.Content.Targets()) == nil {
			t.Fatal("positive transformations overrode failed source check")
		}
	}
}

func TestOversizedLaterTargetStopsBeforeAnyModelCalls(t *testing.T) {
	e, input, _ := repairFixture(t)
	e.Content.Segments[len(e.Content.Segments)-1].Text = strings.Repeat("unbounded input ", 5000)
	m := &budgetReviewer{}
	review, _, err := Verify(context.Background(), m, input, e.Content)
	var oversized *RequestTooLarge
	if !errors.As(err, &oversized) || m.calls != 0 || len(review.Verdicts) != 0 {
		t.Fatalf("preflight should fail without consuming quota: calls=%d err=%v", m.calls, err)
	}
}

func TestFreshGenerationReviewAndEveryRepairFitWithFullWorkingSet(t *testing.T) {
	e, input, _ := repairFixture(t)
	// Five articles, six evidence excerpts, and a full history: the daily
	// working-set limits. Synthetic copies exercise size, not editorial quality.
	for len(input.Evidence) < 6 {
		s := e.Sources[0]
		s.ID = fmt.Sprintf("synthetic-source-%d", len(input.Evidence))
		s.URL = "https://example.com/" + s.ID
		e.Sources = append(e.Sources, s)
		input.Evidence = append(input.Evidence, content.Evidence{Source: s})
	}
	article := e.Content.Articles[0]
	article.ID, article.Headline = "synthetic-story", "A synthetic story for request sizing"
	article.SourceIDs = []string{input.Evidence[4].Source.ID, input.Evidence[5].Source.ID}
	e.Content.Articles = append(e.Content.Articles, article)
	for i := range input.Evidence {
		input.Evidence[i].Text = strings.Repeat("Frozen evidence sentence. ", 100)
	}
	for i := 0; i < 30; i++ {
		input.PreviousHeadlines = append(input.PreviousHeadlines, strings.Repeat("A previous headline ", 12))
	}
	input = PrepareInput(input)
	originalEvidence := append([]content.Evidence{}, input.Evidence...)
	writer := &stagedWriter{draft: e.Content, budget: true}
	var draft content.Draft
	if _, err := GenerateDraft(context.Background(), writer, input, &draft, nil); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(input.Evidence, originalEvidence) {
		t.Fatal("selection mutated writing evidence")
	}
	if err := content.ValidateDraft(draft, e.Sources); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Verify(context.Background(), &budgetReviewer{}, input, draft); err != nil {
		t.Fatal(err)
	}
	rejected := Review{}
	for _, id := range draft.Targets() {
		rejected.Verdicts = append(rejected.Verdicts, Verdict{id, "insufficient_evidence", "A material claim requires correction from supplied premises."})
	}
	repairer := &budgetRepairer{t: t}
	repaired, _, err := Repair(context.Background(), repairer, input, draft, rejected, nil)
	if err != nil {
		t.Fatal(err)
	}
	if repairer.calls != len(draft.Targets()) {
		t.Fatal("repair replay missed targets")
	}
	if _, _, err := Verify(context.Background(), &budgetReviewer{}, input, repaired); err != nil {
		t.Fatal(err)
	}
}

type budgetRepairer struct {
	t     *testing.T
	calls int
}

func (m *budgetRepairer) Generate(_ context.Context, prompt string, in, out any) (Usage, error) {
	m.calls++
	_, estimate, completion, err := requestBody("openai/gpt-oss-120b", prompt, in, out)
	if err != nil {
		return Usage{}, err
	}
	p := in.(repairInput).Packet
	m.t.Logf("repair %s: estimated_input=%d completion=%d", p.Targets[0], estimate, completion)
	switch v := out.(type) {
	case *articleOutput:
		v.Article = p.Draft.Articles[0]
	case *lessonOutput:
		v.Lesson = p.Draft.Lessons[0]
	case *questionOutput:
		v.Question = p.Draft.Quiz[0]
	case *radioOutput:
		v.Segment = p.Draft.Segments[0]
	case *headingOutput:
		*v = headingOutput{p.Draft.Title, p.Draft.Introduction, p.Draft.ArtworkBrief}
	default:
		return Usage{}, fmt.Errorf("unexpected repair output %T", out)
	}
	return Usage{}, nil
}
