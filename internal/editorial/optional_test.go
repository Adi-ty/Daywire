package editorial

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Adi-ty/daywire/internal/content"
)

type optionalWriter struct {
	stagedWriter
	rejected    map[string]bool
	reviews     int
	failID      string
	failErr     error
	quizLessons []string
}

func (m *optionalWriter) Generate(ctx context.Context, prompt string, in, out any) (Usage, error) {
	if review, ok := out.(*Review); ok {
		m.reviews++
		packet := in.(VerificationInput)
		id := packet.Targets[0]
		if id == m.failID {
			return Usage{}, m.failErr
		}
		verdict := "supported"
		if m.rejected[id] {
			verdict = "contradicted"
		}
		review.Verdicts = []Verdict{{id, verdict, "fixture: multiple valid answers or unsupported teaching claim"}}
		return Usage{}, nil
	}
	if packet, ok := in.(repairInput); ok {
		switch o := out.(type) {
		case *lessonOutput:
			o.Lesson = packet.Packet.Draft.Lessons[0]
			o.Lesson.Example = "A corrected example."
		case *questionOutput:
			if !strings.Contains(prompt, "Replace the flawed question and its options completely") {
				return Usage{}, errors.New("quiz repair lacks replacement instruction")
			}
			o.Question = packet.Packet.Draft.Quiz[0]
			o.Question.Prompt = "A rewritten question?"
		case *radioOutput:
			o.Segment = packet.Packet.Draft.Segments[0]
			o.Segment.Text = "A rewritten story."
		case *headingOutput:
			*o = headingOutput{"A rewritten heading", "An introduction.", "A conceptual illustration."}
		default:
			return Usage{}, errors.New("unexpected correction")
		}
		return Usage{}, nil
	}
	if quiz, ok := out.(*quizOutput); ok {
		lesson := in.(content.Lesson)
		m.quizLessons = append(m.quizLessons, lesson.ID)
		start, end := 0, 3
		if m.quiz > 0 {
			start, end = 3, 5
		}
		for _, q := range m.draft.Quiz[start:end] {
			q.LessonID = lesson.ID
			quiz.Quiz = append(quiz.Quiz, q)
		}
		m.quiz++
		m.calls++
		return Usage{}, nil
	}
	return m.stagedWriter.Generate(ctx, prompt, in, out)
}

func TestOptionalContentOmittedWithoutDanglingDependencies(t *testing.T) {
	for _, scenario := range []string{"question", "first lesson", "both lessons", "all questions"} {
		t.Run(scenario, func(t *testing.T) {
			e, input, _ := repairFixture(t)
			rejected := map[string]bool{}
			wantLessons, wantQuiz := 2, 5
			switch scenario {
			case "question":
				rejected[e.Content.Quiz[4].ID] = true
				wantQuiz = 4
			case "first lesson":
				rejected[e.Content.Lessons[0].ID] = true
				wantLessons, wantQuiz = 1, 3
			case "both lessons":
				for _, l := range e.Content.Lessons {
					rejected[l.ID] = true
				}
				wantLessons, wantQuiz = 0, 0
			case "all questions":
				for _, q := range e.Content.Quiz {
					rejected[q.ID] = true
				}
				wantQuiz = 0
			}
			m := &optionalWriter{stagedWriter: stagedWriter{draft: e.Content}, rejected: rejected}
			model := &CachedModel{Model: m, Directory: t.TempDir(), Identity: "test"}
			var draft content.Draft
			_, report, err := GenerateDraftWithReport(context.Background(), model, input, &draft, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(draft.Lessons) != wantLessons || len(draft.Quiz) != wantQuiz {
				t.Fatalf("lessons=%d quiz=%d", len(draft.Lessons), len(draft.Quiz))
			}
			if len(draft.Articles) != len(e.Content.Articles) || len(draft.Segments) != len(draft.Articles) {
				t.Fatal("optional failure removed news or radio")
			}
			for _, id := range draft.Targets() {
				if rejected[id] {
					t.Fatalf("retained rejected target %s", id)
				}
			}
			for _, id := range m.quizLessons {
				if rejected[id] {
					t.Fatal("generated quiz from rejected lesson")
				}
			}
			if err := content.ValidateDraft(draft, e.Sources); err != nil {
				t.Fatal(err)
			}
			if report.Status != "draft_complete" || report.Retained.Questions != wantQuiz || len(report.Omissions) != len(rejected) {
				t.Fatalf("bad report: %+v", report)
			}
			for _, omission := range report.Omissions {
				if !rejected[omission.TargetID] || omission.Reason == "" || omission.Verdict != "contradicted" {
					t.Fatalf("bad omission: %+v", omission)
				}
			}
			calls, reviews := m.calls, m.reviews
			_, resumed, err := GenerateDraftWithReport(context.Background(), model, input, &draft, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(resumed.Omissions) != len(report.Omissions) {
				t.Fatal("lost report on resume")
			}
			if _, _, err := Verify(context.Background(), model, input, draft); err != nil {
				t.Fatal(err)
			}
			if m.calls != calls || m.reviews != reviews {
				t.Fatal("repeated paid work or rerolled rejection")
			}
		})
	}
}

func TestOptionalContentDoesNotSwallowOperationalErrors(t *testing.T) {
	for _, kind := range []string{"lesson", "question"} {
		for _, failure := range []error{errors.New("provider unavailable"), context.Canceled, &CoverageError{}} {
			e, input, _ := repairFixture(t)
			id := e.Content.Lessons[0].ID
			if kind == "question" {
				id = e.Content.Quiz[0].ID
			}
			m := &optionalWriter{stagedWriter: stagedWriter{draft: e.Content}, failID: id, failErr: failure}
			var draft content.Draft
			_, report, err := GenerateDraftWithReport(context.Background(), m, input, &draft, nil)
			if err == nil || report.Status != "incomplete" || report.Error == "" || len(report.Omissions) != 0 || m.radio != 0 {
				t.Fatalf("operational error became omission: %+v %v", report, err)
			}
		}
	}
}

func TestMandatoryHeadingRejectionStillStops(t *testing.T) {
	e, input, _ := repairFixture(t)
	m := &optionalWriter{stagedWriter: stagedWriter{draft: e.Content}, rejected: map[string]bool{"edition": true}}
	var draft content.Draft
	_, report, err := GenerateDraftWithReport(context.Background(), m, input, &draft, nil)
	var rejection *exhaustedTarget
	if !errors.As(err, &rejection) || report.Status != "incomplete" || report.Error == "" || len(report.Omissions) != 0 {
		t.Fatalf("mandatory rejection was ignored: %v %+v", err, report)
	}
}
