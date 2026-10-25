package editorial

import (
	"context"
	"errors"
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
func TestFailedArticleGateStopsAndCannotRerollOnResume(t *testing.T) {
	e, input, _ := repairFixture(t)
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
			text += s.Text
		}
		if strings.Count(text, "Welcome to Daywire") != 1 || strings.Count(text, "Thanks for listening") != 1 || strings.Count(text, "October twenty four, two thousand twenty six") != 1 {
			t.Fatal(text)
		}
	}
	if radioBodyError("Welcome to Daywire. October twenty six.") == nil {
		t.Fatal("model-generated framing accepted")
	}
}
