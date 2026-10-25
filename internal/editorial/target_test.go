package editorial

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Adi-ty/daywire/internal/content"
)

type gatedWriter struct {
	stagedWriter
	rejectedID  string
	permanent   bool
	corrected   bool
	reviewCalls int
}

func (m *gatedWriter) Generate(ctx context.Context, prompt string, in, out any) (Usage, error) {
	if r, ok := out.(*Review); ok {
		m.reviewCalls++
		packet := in.(VerificationInput)
		if len(packet.Targets) != 1 {
			return Usage{}, errors.New("review is not isolated")
		}
		verdict := "supported"
		if packet.Targets[0] == m.rejectedID && (!m.corrected || m.permanent) {
			verdict = "insufficient_evidence"
		}
		r.Verdicts = []Verdict{{packet.Targets[0], verdict, "fixture evidence judgment"}}
		return Usage{}, nil
	}
	if packet, ok := in.(repairInput); ok {
		switch o := out.(type) {
		case *articleOutput:
			if !strings.Contains(prompt, Prompt("article")) {
				return Usage{}, errors.New("article repair rules missing")
			}
			o.Article = packet.Packet.Draft.Articles[0]
			o.Article.Summary = "Corrected supported summary"
		case *lessonOutput:
			if !strings.Contains(prompt, lessonRepairRules) {
				return Usage{}, errors.New("lesson repair rules missing")
			}
			o.Lesson = packet.Packet.Draft.Lessons[0]
			o.Lesson.Example = "Corrected supported example"
		default:
			return Usage{}, errors.New("unexpected correction")
		}
		m.corrected = true
		return Usage{}, nil
	}
	if _, ok := out.(*lessonOutput); ok && m.rejectedID == m.draft.Articles[0].ID {
		if !m.corrected {
			return Usage{}, errors.New("lesson generated before article correction")
		}
	}
	return m.stagedWriter.Generate(ctx, prompt, in, out)
}

func TestGatesRepairBeforeDependenciesAndResume(t *testing.T) {
	for _, kind := range []string{"article", "lesson"} {
		t.Run(kind, func(t *testing.T) {
			e, input, _ := repairFixture(t)
			id := e.Content.Articles[0].ID
			if kind == "lesson" {
				id = e.Content.Lessons[0].ID
			}
			m := &gatedWriter{stagedWriter: stagedWriter{draft: e.Content}, rejectedID: id}
			cache := &CachedModel{Model: m, Directory: t.TempDir(), Identity: "test"}
			var draft content.Draft
			if _, err := GenerateDraft(context.Background(), cache, input, &draft, nil); err != nil {
				t.Fatal(err)
			}
			if !m.corrected {
				t.Fatal("correction did not run")
			}
			if kind == "article" && m.radioInputs[0].Article.Summary != "Corrected supported summary" {
				t.Fatal("radio used rejected article")
			}
			calls, reviews := m.calls, m.reviewCalls
			if _, err := GenerateDraft(context.Background(), cache, input, &draft, nil); err != nil {
				t.Fatal(err)
			}
			if _, _, err := Verify(context.Background(), cache, input, draft); err != nil {
				t.Fatal(err)
			}
			if m.calls != calls || m.reviewCalls != reviews {
				t.Fatal("resume or final verification repeated completed work")
			}
		})
	}
}
func TestNoAcceptedArticlesStopsAndCannotRerollOnResume(t *testing.T) {
	e, input, _ := repairFixture(t)
	e.Content.Articles = e.Content.Articles[:1]
	m := &gatedWriter{stagedWriter: stagedWriter{draft: e.Content}, rejectedID: e.Content.Articles[0].ID, permanent: true}
	cache := &CachedModel{Model: m, Directory: t.TempDir(), Identity: "test"}
	var draft content.Draft
	for run := 0; run < 2; run++ {
		if _, err := GenerateDraft(context.Background(), cache, input, &draft, nil); err == nil {
			t.Fatal("unsupported article accepted")
		}
	}
	if m.lesson != 0 || m.radio != 0 || m.reviewCalls != 2 {
		t.Fatalf("dependent work or repeated judgment: %+v", m)
	}
}
func TestRadioFrameHasOneTrustedDateAndClosing(t *testing.T) {
	for total := 1; total <= 5; total++ {
		var text string
		for position := 1; position <= total; position++ {
			s := frameRadio(content.Segment{Text: "A supported story."}, "2026-10-24", position, total)
			if position == 1 && !strings.Contains(s.Text, "We begin with our lead story.") {
				t.Fatal("missing lead introduction", s.Text)
			}
			if position > 1 && position == total && !strings.HasPrefix(s.Text, "And finally, our last story.") {
				t.Fatal("missing final story handoff", s.Text)
			}
			if position > 1 && position < total && !strings.Contains(s.Text, "next") && !strings.Contains(s.Text, "Next") {
				t.Fatal("missing story handoff", s.Text)
			}
			text += s.Text
		}
		if strings.Count(text, "welcome to Daywire") != 1 || strings.Count(text, "Thanks for listening") != 1 || strings.Count(text, "October twenty four, two thousand twenty six") != 1 {
			t.Fatal(text)
		}
	}
	if radioBodyError("Welcome to Daywire. October twenty six.") == nil {
		t.Fatal("model-generated framing accepted")
	}
}

// Build fixture lessons from the actual accepted premise, and reject any leak
// of an omitted article into a dependent writing packet.
type omissionWriter struct {
	gatedWriter
	reviewErr error
}

func (m *omissionWriter) Generate(ctx context.Context, prompt string, in, out any) (Usage, error) {
	if _, ok := out.(*Review); ok && m.reviewErr != nil {
		return Usage{}, m.reviewErr
	}
	switch out.(type) {
	case *lessonOutput, *radioOutput, *headingOutput:
		if _, repair := in.(repairInput); !repair {
			b, _ := json.Marshal(in)
			if strings.Contains(string(b), m.rejectedID) {
				return Usage{}, fmt.Errorf("rejected article leaked into dependent input: %s", m.rejectedID)
			}
			if _, lesson := out.(*lessonOutput); lesson {
				var packet struct {
					Packet VerificationInput `json:"packet"`
				}
				if err := json.Unmarshal(b, &packet); err != nil {
					return Usage{}, err
				}
				a := packet.Packet.Draft.Articles[0]
				m.draft.Lessons[m.lesson].ArticleIDs = []string{a.ID}
				m.draft.Lessons[m.lesson].SourceIDs = a.SourceIDs
			}
		}
	}
	return m.gatedWriter.Generate(ctx, prompt, in, out)
}

func TestRejectedArticleOmittedBeforeDependenciesAndStableOnResume(t *testing.T) {
	e, _, _ := repairFixture(t)
	for index := range e.Content.Articles {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			e, input, _ := repairFixture(t)
			id := e.Content.Articles[index].ID
			m := &omissionWriter{gatedWriter: gatedWriter{stagedWriter: stagedWriter{draft: e.Content}, rejectedID: id, permanent: true}}
			cache := &CachedModel{Model: m, Directory: t.TempDir(), Identity: "test"}
			var draft content.Draft
			var logs []string
			logf := func(format string, args ...any) { logs = append(logs, fmt.Sprintf(format, args...)) }
			if _, err := GenerateDraft(context.Background(), cache, input, &draft, logf); err != nil {
				t.Fatal(err)
			}
			if len(draft.Articles) != len(e.Content.Articles)-1 || len(draft.Segments) != len(draft.Articles) {
				t.Fatal("wrong retained story count")
			}
			if !strings.Contains(strings.Join(logs, "\n"), "Omitting article "+id) {
				t.Fatal("omission not reported")
			}
			if err := content.ValidateDraft(draft, e.Sources); err != nil {
				t.Fatal(err)
			}
			for i, packet := range m.radioInputs {
				if packet.Position != i+1 || packet.Total != len(draft.Articles) {
					t.Fatal("radio position includes omitted article")
				}
			}
			calls, reviews := m.calls, m.reviewCalls
			if _, err := GenerateDraft(context.Background(), cache, input, &draft, nil); err != nil {
				t.Fatal(err)
			}
			if _, _, err := Verify(context.Background(), cache, input, draft); err != nil {
				t.Fatal(err)
			}
			if calls != m.calls || reviews != m.reviewCalls {
				t.Fatal("resume rerolled completed judgments")
			}
		})
	}
}

func TestArticleReviewInfrastructureErrorCannotBecomeOmission(t *testing.T) {
	e, input, _ := repairFixture(t)
	for _, failure := range []error{errors.New("network unavailable"), context.Canceled, &CoverageError{}} {
		m := &omissionWriter{gatedWriter: gatedWriter{stagedWriter: stagedWriter{draft: e.Content}}, reviewErr: failure}
		var draft content.Draft
		_, err := GenerateDraft(context.Background(), m, input, &draft, nil)
		if err == nil || m.lesson != 0 || m.radio != 0 {
			t.Fatal("review failure did not stop generation", err)
		}
	}
}
