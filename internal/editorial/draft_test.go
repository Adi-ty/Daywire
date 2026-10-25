package editorial

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Adi-ty/daywire/internal/content"
	"github.com/Adi-ty/daywire/internal/store"
)

type stagedWriter struct {
	draft                               content.Draft
	article, lesson, radio, quiz, calls int
	failAt                              int
	budget                              bool
	groups                              [][]string
	articleInputs                       []Input
	radioInputs                         []radioWritingInput
}

func (m *stagedWriter) Generate(_ context.Context, system string, in, out any) (Usage, error) {
	m.calls++
	if m.calls == m.failAt {
		return Usage{}, errors.New("network unavailable")
	}
	if _, ok := out.(*content.Draft); ok {
		return Usage{}, errors.New("monolithic drafting is not allowed")
	}
	if m.budget {
		if _, _, _, err := requestBody("openai/gpt-oss-120b", system, in, out); err != nil {
			return Usage{}, err
		}
	}
	switch v := out.(type) {
	case *Review:
		for _, id := range in.(VerificationInput).Targets {
			v.Verdicts = append(v.Verdicts, Verdict{id, "supported", "Frozen evidence supports this target"})
		}
	case *selectionOutput:
		if m.groups != nil {
			v.Groups = m.groups
		} else {
			for _, a := range m.draft.Articles {
				v.Groups = append(v.Groups, a.SourceIDs)
			}
		}
	case *articleOutput:
		m.articleInputs = append(m.articleInputs, in.(Input))
		v.Article = m.draft.Articles[m.article]
		m.article++
	case *lessonOutput:
		v.Lesson = m.draft.Lessons[m.lesson]
		m.lesson++
	case *quizOutput:
		if m.quiz == 0 {
			v.Quiz = m.draft.Quiz[:3]
		} else {
			v.Quiz = m.draft.Quiz[3:]
		}
		m.quiz++
	case *radioOutput:
		m.radioInputs = append(m.radioInputs, in.(radioWritingInput))
		a := m.draft.Articles[m.radio]
		v.Segment = content.Segment{ID: "radio-" + a.ID, Title: a.Headline, Text: a.Summary, ArticleIDs: []string{a.ID}}
		m.radio++
	case *headingOutput:
		*v = headingOutput{m.draft.Title, m.draft.Introduction, m.draft.ArtworkBrief}
	default:
		return Usage{}, errors.New("unexpected output")
	}
	return Usage{TotalTokens: 10}, nil
}

func TestSelectionBoundsStorySourcesBeforeWriting(t *testing.T) {
	e, input, _ := repairFixture(t)
	ids := []string{}
	for _, ev := range input.Evidence {
		ids = append(ids, ev.Source.ID)
	}
	e.Content.Articles = e.Content.Articles[:1]
	e.Content.Articles[0].SourceIDs = ids[:2]
	for i := range e.Content.Lessons {
		e.Content.Lessons[i].ArticleIDs = []string{e.Content.Articles[0].ID}
		e.Content.Lessons[i].SourceIDs = ids[:2]
	}
	model := &stagedWriter{draft: e.Content, groups: [][]string{ids}, budget: true}
	var draft content.Draft
	if _, err := GenerateDraft(context.Background(), model, input, &draft, nil); err != nil {
		t.Fatal(err)
	}
	if len(model.articleInputs) != 1 || len(model.articleInputs[0].Evidence) != 2 {
		t.Fatal("unbounded source fan-in reached the article writer")
	}
	for i, ev := range model.articleInputs[0].Evidence {
		if ev.Source.ID != ids[i] || ev.Text != input.Evidence[i].Text {
			t.Fatal("selected source text was truncated or replaced")
		}
	}
	if err := content.ValidateDraft(draft, e.Sources); err != nil {
		t.Fatal(err)
	}
	// A writer cannot expand its source set beyond the actual supplied packet.
	e.Content.Articles[0].SourceIDs = ids[2:3]
	model = &stagedWriter{draft: e.Content, groups: [][]string{ids}, budget: true}
	if _, err := GenerateDraft(context.Background(), model, input, &draft, nil); err == nil {
		t.Fatal("article cited a source it was not given")
	}
}
func TestDraftStagesKeepEditionContractAndResumeWithoutCalls(t *testing.T) {
	var e content.Edition
	if err := store.Read("../../testdata/demo/2026-10-23.json", &e); err != nil {
		t.Fatal(err)
	}
	input := Input{Date: e.Date, Evidence: []content.Evidence{}, PreviousHeadlines: []string{}}
	for _, s := range e.Sources {
		input.Evidence = append(input.Evidence, content.Evidence{Source: s, Text: "Frozen evidence"})
	}
	m := &stagedWriter{draft: e.Content, budget: true}
	cache := &CachedModel{Model: m, Directory: t.TempDir(), Identity: "groq:test"}
	var draft content.Draft
	usage, err := GenerateDraft(context.Background(), cache, input, &draft, nil)
	if err != nil {
		t.Fatal(err)
	}
	if usage.TotalTokens == 0 {
		t.Fatal("lost stage usage")
	}
	if err := content.ValidateDraft(draft, e.Sources); err != nil {
		t.Fatal(err)
	}
	for i, packet := range m.radioInputs {
		if packet.Position != i+1 || packet.Total != len(draft.Articles) || packet.Article.ID != draft.Articles[i].ID {
			t.Fatal("radio lost its show position or factual premise")
		}
		if i == 0 && packet.PreviousEnding != "" {
			t.Fatal("first segment has unrelated context")
		}
		if i > 0 {
			if packet.PreviousEnding == "" || len(strings.Fields(packet.PreviousEnding)) > 35 || !strings.HasSuffix(draft.Segments[i-1].Text, packet.PreviousEnding) {
				t.Fatal("radio continuity context is missing, unbounded or incorrect")
			}
		}
	}
	calls := m.calls
	if _, err := GenerateDraft(context.Background(), cache, input, &draft, nil); err != nil {
		t.Fatal(err)
	}
	if m.calls != calls {
		t.Fatalf("repeated successful work: %d -> %d", calls, m.calls)
	}
}
