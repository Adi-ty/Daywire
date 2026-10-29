package editorial

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Adi-ty/daywire/internal/content"
)

func TestBroadSelectionConsidersEveryBatchWithinBudget(t *testing.T) {
	input := Input{Date: "2026-10-26"}
	for i := 0; i < 36; i++ {
		input.Evidence = append(input.Evidence, content.Evidence{HN: &content.HNEngagement{Points: 1500, Comments: 900}, Source: content.Source{
			ID: fmt.Sprintf("source-%02d", i), Title: strings.Repeat("A developer-facing improvement ", 8), Name: "An engineering publisher",
		}, Text: strings.Repeat("A supported mechanism and an important limitation. ", 100), Kind: "article_excerpt_truncated"})
	}
	for i := 0; i < 30; i++ {
		input.PreviousHeadlines = append(input.PreviousHeadlines, strings.Repeat("A previous headline ", 12))
	}
	seen, calls := map[string]bool{}, 0
	out, err := selectStories(input, func(label, prompt string, in, out any) error {
		calls++
		p := in.(selectionPacket)
		if _, _, _, err := requestBody("openai/gpt-oss-120b", stageRules+prompt, p, out); err != nil {
			return err
		}
		result := out.(*selectionOutput)
		if strings.HasPrefix(label, "shortlist") {
			if len(p.Stories) > 8 {
				t.Fatal("oversized shortlist")
			}
			for _, s := range p.Stories {
				seen[s.ID] = true
			}
		}
		for _, s := range p.Stories[:min(p.Limit, len(p.Stories))] {
			result.Groups = append(result.Groups, []string{s.ID})
		}
		return validStage(p, result)
	})
	if err != nil || calls != 6 || len(seen) != 36 || len(out.Groups) != 12 {
		t.Fatalf("calls=%d seen=%d groups=%d err=%v", calls, len(seen), len(out.Groups), err)
	}
}

func TestSelectionAndOrderingRejectUnknownDuplicateOrMissingIDs(t *testing.T) {
	p := selectionPacket{Limit: 2, Stories: []storyPreview{{ID: "one"}, {ID: "two"}}}
	for _, groups := range [][][]string{{{}}, {{"invented"}}, {{"one"}, {"one"}}, {{"one"}, {"two"}, {"three"}}} {
		if validStage(p, &selectionOutput{Groups: groups}) == nil {
			t.Fatalf("accepted %v", groups)
		}
	}
	articles := []articlePreview{{ID: "one"}, {ID: "two"}}
	for _, ids := range [][]string{{"one"}, {"one", "one"}, {"one", "unknown"}} {
		if validStage(articles, &rankingOutput{ids}) == nil {
			t.Fatalf("accepted ranking %v", ids)
		}
	}
	if err := validStage(articles, &rankingOutput{[]string{"two", "one"}}); err != nil {
		t.Fatal(err)
	}
}

func TestRicherEvidenceAndFullEditionFitWritingReviewAndRepair(t *testing.T) {
	e, input, _ := repairFixture(t)
	for i := range input.Evidence {
		input.Evidence[i].Text = strings.Repeat("Specific evidence and qualifications. ", 100)
	}
	input = PrepareInput(input)
	// Two-source lead, with realistic long copy, plus a lesson that cites both.
	a := e.Content.Articles[0]
	a.SourceIDs = []string{input.Evidence[0].Source.ID, input.Evidence[1].Source.ID}
	a.Summary = strings.Repeat("A supported summary. ", 12)
	a.Paragraphs = []string{strings.Repeat("A supported mechanism with its limitation. ", 20)}
	a.Implication = strings.Repeat("A conditional interpretation. ", 8)
	e.Content.Articles[0] = a
	e.Content.Lessons[0].ArticleIDs = []string{a.ID}
	e.Content.Lessons[0].SourceIDs = a.SourceIDs
	e.Content.Lessons[0].Paragraphs = []string{strings.Repeat("The underlying mechanism has an important qualification. ", 16)}
	for _, id := range []string{a.ID, e.Content.Lessons[0].ID} {
		packet := reviewSubset(input, e.Content, []string{id})
		if _, _, _, err := requestBody("openai/gpt-oss-120b", verificationPrompt(packet), packet, &Review{}); err != nil {
			t.Fatalf("review %s: %v", id, err)
		}
		var output any = &articleOutput{}
		prompt := repairRules + Prompt("article")
		if id != a.ID {
			output = &lessonOutput{}
			prompt = repairRules + lessonRepairRules
		}
		if _, _, _, err := requestBody("openai/gpt-oss-120b", prompt, repairInput{Packet: packet, Feedback: []Verdict{{id, "insufficient_evidence", "An unsupported claim needs a correction."}}}, output); err != nil {
			t.Fatalf("repair %s: %v", id, err)
		}
	}
	for len(e.Content.Articles) < 12 {
		copy := a
		copy.ID = fmt.Sprintf("article-extra-%d", len(e.Content.Articles))
		e.Content.Articles = append(e.Content.Articles, copy)
	}
	for i := 0; i < 30; i++ {
		input.PreviousHeadlines = append(input.PreviousHeadlines, strings.Repeat("Previous headline ", 12))
	}
	packet := reviewSubset(input, e.Content, []string{"edition"})
	if _, _, _, err := requestBody("openai/gpt-oss-120b", verificationPrompt(packet), packet, &Review{}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := requestBody("openai/gpt-oss-120b", stageRules, rankingInput(e.Content.Articles), &rankingOutput{}); err != nil {
		t.Fatal(err)
	}
}

func TestEvidenceExcerptRetainsLaterLimitsWithoutInventingText(t *testing.T) {
	opening := "The library now supports concurrent uploads."
	filler := strings.Repeat("Background context without new technical details. ", 30)
	qualification := "However, concurrent uploads are experimental and require version 2.1. Only Linux is supported."
	original := opening + "\n\n" + filler + "\n\n" + qualification
	got := evidenceExcerpt(original, "Concurrent uploads in version 2.1", 500)
	if !strings.HasPrefix(got, opening) || !strings.Contains(got, qualification) || !strings.Contains(got, "[...]") || len([]rune(got)) > 500 {
		t.Fatal(got)
	}
	for _, paragraph := range strings.Split(got, "\n\n") {
		if paragraph != "[...]" && !strings.Contains(original, paragraph) {
			t.Fatal("invented evidence")
		}
	}
}

func TestExpandedNewspaperKeepsRadioBoundedAndStagesResumable(t *testing.T) {
	e, input, _ := repairFixture(t)
	original := e.Content.Articles[0]
	e.Content.Articles = []content.Article{}
	input.Evidence = nil
	for i := 0; i < 36; i++ {
		source := content.Evidence{Source: e.Sources[0], Text: strings.Repeat("Evidence with limitations. ", 100)}
		source.Source.ID = fmt.Sprintf("source-expanded-%d", i)
		input.Evidence = append(input.Evidence, source)
	}
	chosen := []int{0, 1, 2, 3, 8, 9, 10, 11, 16, 17, 18, 19}
	for i, sourceIndex := range chosen {
		a := original
		a.ID = fmt.Sprintf("story-%d", i)
		a.Headline = fmt.Sprintf("A distinct development %d", i)
		source := input.Evidence[sourceIndex]
		a.SourceIDs = []string{source.Source.ID}
		e.Content.Articles = append(e.Content.Articles, a)
	}
	// Exercise all downstream stages with an expanded, already selected pool;
	// selection coverage and request bounds are tested separately above.
	for i := range e.Content.Lessons {
		e.Content.Lessons[i].ArticleIDs = []string{e.Content.Articles[i].ID}
		e.Content.Lessons[i].SourceIDs = e.Content.Articles[i].SourceIDs
	}
	m := &expandedWriter{stagedWriter: stagedWriter{draft: e.Content, budget: true}}
	cache := &CachedModel{Model: m, Directory: t.TempDir(), Identity: "newspaper-test"}
	var draft content.Draft
	if _, err := GenerateDraft(context.Background(), cache, PrepareInput(input), &draft, nil); err != nil {
		t.Fatal(err)
	}
	if len(draft.Articles) != 12 || len(draft.Segments) != 5 {
		t.Fatalf("articles=%d radio=%d", len(draft.Articles), len(draft.Segments))
	}
	if !strings.Contains(draft.Segments[4].Text, "that's your Daywire briefing") {
		t.Fatal("bounded radio lost its signoff")
	}
	calls := m.calls
	if _, err := GenerateDraft(context.Background(), cache, PrepareInput(input), &draft, nil); err != nil {
		t.Fatal(err)
	}
	if m.calls != calls {
		t.Fatal("resume repeated generation")
	}
}

type expandedWriter struct{ stagedWriter }

func (m *expandedWriter) Generate(ctx context.Context, prompt string, in, out any) (Usage, error) {
	if selection, ok := out.(*selectionOutput); ok {
		m.calls++
		p := in.(selectionPacket)
		for _, s := range p.Stories {
			if strings.HasPrefix(s.ID, "source-expanded-") && len(selection.Groups) < p.Limit {
				selection.Groups = append(selection.Groups, []string{s.ID})
			}
		}
		return Usage{}, nil
	}
	return m.stagedWriter.Generate(ctx, prompt, in, out)
}

// Signals must survive grouping under a primary publisher, preparation, and final
// ordering; a second observation must not double-count the same audience.
func TestHNEngagementSurvivesSelectionAndRanking(t *testing.T) {
	input := Input{Date: "2026-10-29"}
	for i := 0; i < 9; i++ {
		input.Evidence = append(input.Evidence, content.Evidence{
			Source: content.Source{ID: fmt.Sprintf("source-%d", i)}, Text: "Supported reporting.",
		})
	}
	input.Evidence[1].HN = &content.HNEngagement{Points: 600, Comments: 250}
	input = PrepareInput(input)
	finalSeen := false
	_, err := selectStories(input, func(label, prompt string, in, out any) error {
		p := in.(selectionPacket)
		if !strings.Contains(prompt, engagementRules) {
			t.Fatal("engagement policy missing")
		}
		result := out.(*selectionOutput)
		switch label {
		case "shortlist 1/2":
			if p.Stories[0].HN != nil || p.Stories[1].HN.Points != 600 {
				t.Fatal("missing or invented signals")
			}
			result.Groups = [][]string{{"source-0", "source-1"}}
		case "shortlist 2/2":
			result.Groups = [][]string{{"source-8"}}
		case "selection":
			finalSeen = true
			if p.Stories[0].HN == nil || p.Stories[0].HN.Points != 600 {
				t.Fatal("companion signal lost")
			}
			result.Groups = [][]string{{"source-0"}}
		}
		return nil
	})
	if err != nil || !finalSeen {
		t.Fatalf("selection: %v", err)
	}
	articles := []content.Article{{ID: "story", SourceIDs: []string{"source-0", "source-1"}}}
	ranked := rankingInput(articles, input.Evidence...)
	if ranked[0].HN == nil || ranked[0].HN.Points != 600 {
		t.Fatal("ranking lost engagement")
	}
	if input.Evidence[0].HN != nil {
		t.Fatal("selection mutated original evidence")
	}
	// Old snapshots remain readable and do not pretend zero means unpopular.
	legacy := previews(Input{Evidence: []content.Evidence{{Source: content.Source{ID: "old"}}}}, 12)
	if legacy.Stories[0].HN != nil {
		t.Fatal("invented legacy engagement")
	}
}

type engagementSelectionModel struct{ calls int }

func (m *engagementSelectionModel) Generate(_ context.Context, _ string, in, out any) (Usage, error) {
	m.calls++
	p := in.(selectionPacket)
	out.(*selectionOutput).Groups = [][]string{{p.Stories[0].ID}}
	return Usage{}, nil
}

func TestSelectionCacheIncludesHNObservation(t *testing.T) {
	backend := &engagementSelectionModel{}
	cache := &CachedModel{Model: backend, Directory: t.TempDir(), Identity: "test"}
	input := Input{Evidence: []content.Evidence{{Source: content.Source{ID: "one"}}}}
	run := func() {
		t.Helper()
		var result selectionOutput
		if _, err := cache.Generate(context.Background(), stageRules+selectionRules, previews(input, 12), &result); err != nil {
			t.Fatal(err)
		}
	}
	run() // Legacy snapshot with no signals.
	input.Evidence[0].HN = &content.HNEngagement{Points: 600, Comments: 200}
	run() // Adding signals must invalidate selection, even though evidence text is unchanged.
	run() // Identical observation should reuse the cache.
	if backend.calls != 2 {
		t.Fatalf("calls=%d, want 2", backend.calls)
	}
}

func TestEngagementRankingFitsRequestBudget(t *testing.T) {
	var articles []content.Article
	var evidence []content.Evidence
	for i := 0; i < 12; i++ {
		id := fmt.Sprintf("source-%d", i)
		articles = append(articles, content.Article{ID: fmt.Sprintf("article-%d", i), SourceIDs: []string{id},
			Headline: strings.Repeat("Headline ", 20), Summary: strings.Repeat("Summary ", 40), Implication: strings.Repeat("Impact ", 40)})
		evidence = append(evidence, content.Evidence{Source: content.Source{ID: id}, HN: &content.HNEngagement{Points: 1500, Comments: 1000}})
	}
	packet := rankingInput(articles, evidence...)
	if _, _, _, err := requestBody("openai/gpt-oss-120b", stageRules+engagementRules+"Order every article ID exactly once.", packet, &rankingOutput{}); err != nil {
		t.Fatal(err)
	}
}
