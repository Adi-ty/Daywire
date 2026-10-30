package editorial

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Adi-ty/daywire/internal/content"
)

func TestRadioOmissionRetainsNewspaperAndFramesAcceptedShow(t *testing.T) {
	for _, scenario := range []string{"first", "middle", "last", "only one survives"} {
		t.Run(scenario, func(t *testing.T) {
			e, input, _ := repairFixture(t)
			rejected := map[string]bool{}
			n := min(5, len(e.Content.Articles))
			if n < 3 {
				t.Fatal("fixture needs at least three articles")
			}
			for i, a := range e.Content.Articles[:n] {
				if scenario == "first" && i == 0 || scenario == "middle" && i == 1 || scenario == "last" && i == n-1 || scenario == "only one survives" && i != 1 {
					rejected["radio-"+a.ID] = true
				}
			}
			m := &optionalWriter{stagedWriter: stagedWriter{draft: e.Content}, rejected: rejected}
			cache := &CachedModel{Model: m, Directory: t.TempDir(), Identity: "radio-omission"}
			var draft content.Draft
			_, report, err := GenerateDraftWithReport(context.Background(), cache, input, &draft, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(draft.Articles, e.Content.Articles) || len(draft.Lessons) != 2 || len(draft.Quiz) != 5 {
				t.Fatal("radio rejection changed the newspaper")
			}
			if len(draft.Segments) != n-len(rejected) || len(report.Omissions) != len(rejected) || report.Retained.Segments != len(draft.Segments) {
				t.Fatalf("bad retained counts: %+v", report)
			}
			for _, omission := range report.Omissions {
				if omission.Kind != "radio segment" || !rejected[omission.TargetID] || omission.Verdict != "contradicted" || omission.Reason == "" {
					t.Fatalf("bad omission: %+v", omission)
				}
			}
			for i, s := range draft.Segments {
				if rejected[s.ID] {
					t.Fatal("rejected story survived")
				}
				var article content.Article
				for _, a := range draft.Articles {
					if a.ID == s.ArticleIDs[0] {
						article = a
					}
				}
				want := frameRadio(content.Segment{Text: article.Summary}, input.Date, i+1, len(draft.Segments))
				if s.Text != want.Text {
					t.Fatalf("wrong framing at %d: %s", i, s.Text)
				}
			}
			lastEnding := ""
			for _, packet := range m.radioInputs {
				if packet.PreviousEnding != lastEnding {
					t.Fatal("continuity used a rejected story")
				}
				if !rejected["radio-"+packet.Article.ID] {
					words := strings.Fields(packet.Article.Summary)
					lastEnding = strings.Join(words[max(0, len(words)-35):], " ")
				}
			}
			if err := content.ValidateDraft(draft, e.Sources); err != nil {
				t.Fatal(err)
			}
			if _, _, err := Verify(context.Background(), cache, input, draft); err != nil {
				t.Fatal(err)
			}
			calls, reviews := m.calls, m.reviews
			var resumed content.Draft
			_, resumedReport, err := GenerateDraftWithReport(context.Background(), cache, input, &resumed, nil)
			if err != nil || !reflect.DeepEqual(draft, resumed) || !reflect.DeepEqual(report, resumedReport) {
				t.Fatalf("resume changed the show or omissions: %v", err)
			}
			if _, _, err := Verify(context.Background(), cache, input, resumed); err != nil {
				t.Fatal(err)
			}
			if m.calls != calls || m.reviews != reviews {
				t.Fatal("resume repeated generation or rerolled a factual rejection")
			}
		})
	}
}

func TestRadioOmissionDoesNotHideOperationalErrors(t *testing.T) {
	for _, failure := range []error{errors.New("provider unavailable"), context.Canceled, &CoverageError{}} {
		e, input, _ := repairFixture(t)
		m := &optionalWriter{stagedWriter: stagedWriter{draft: e.Content}, failID: "radio-" + e.Content.Articles[0].ID, failErr: failure}
		var draft content.Draft
		_, report, err := GenerateDraftWithReport(context.Background(), m, input, &draft, nil)
		if !errors.Is(err, failure) || report.Status != "incomplete" || len(report.Omissions) != 0 || len(draft.Articles) != 0 {
			t.Fatalf("operational error was swallowed: %v %+v", err, report)
		}
	}
}

func TestAllRadioStoriesRejectedCannotPublishEmptyShow(t *testing.T) {
	e, input, _ := repairFixture(t)
	rejected := map[string]bool{}
	for _, a := range e.Content.Articles {
		rejected["radio-"+a.ID] = true
	}
	m := &optionalWriter{stagedWriter: stagedWriter{draft: e.Content}, rejected: rejected}
	cache := &CachedModel{Model: m, Directory: t.TempDir(), Identity: "all-radio-rejected"}
	var draft content.Draft
	_, report, err := GenerateDraftWithReport(context.Background(), cache, input, &draft, nil)
	if err == nil || !strings.Contains(err.Error(), "no radio stories passed") || report.Retained.Segments != 0 || report.Status != "incomplete" || len(draft.Articles) != 0 {
		t.Fatalf("empty show accepted: %v %+v", err, report)
	}
	calls, reviews := m.calls, m.reviews
	if _, _, err := GenerateDraftWithReport(context.Background(), cache, input, &draft, nil); err == nil || m.calls != calls || m.reviews != reviews {
		t.Fatal("resume rerolled rejected stories")
	}
}

func TestRadioReviewRemovesOnlyExactTrustedFraming(t *testing.T) {
	const body = "The model uses one hundred twenty four thousand output tokens."
	const date = "2026-10-30"
	for total := 1; total <= 5; total++ {
		for position := 1; position <= total; position++ {
			framed := frameRadio(content.Segment{Text: body}, date, position, total).Text
			if got := radioReviewBody(framed, date, position, total); got != body {
				t.Fatalf("factual content changed: %s", got)
			}
			if got := radioReviewBody(body, date, position, total); got != body {
				t.Fatal("unframed body changed")
			}
			altered := "An unsupported claim. " + framed
			if got := radioReviewBody(altered, date, position, total); got != altered {
				t.Fatal("altered frame hidden from review")
			}
			if position == 1 && radioReviewBody(framed, "2026-10-31", position, total) != framed {
				t.Fatal("incorrect broadcast date hidden from review")
			}
			if position == total {
				altered = framed + " The release is free."
				if radioReviewBody(altered, date, position, total) != altered {
					t.Fatal("claim added after sign-off hidden from review")
				}
			}
		}
	}
}
