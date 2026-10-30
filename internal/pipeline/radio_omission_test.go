package pipeline

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Adi-ty/daywire/internal/content"
	"github.com/Adi-ty/daywire/internal/editorial"
	"github.com/Adi-ty/daywire/internal/store"
)

// Reproduce a radio writer that changes 123k to 124k and repeats the mistake
// during correction. The source article remains valid and publishable.
type wrongNumberRadio struct {
	*fakeModel
	articleID string
	reviews   int
	repairs   int
}

func (m *wrongNumberRadio) Generate(ctx context.Context, prompt string, input, out any) (editorial.Usage, error) {
	if review, ok := out.(*editorial.Review); ok {
		packet := input.(editorial.VerificationInput)
		if len(packet.Draft.Segments) == 1 && packet.Draft.Segments[0].ID == "radio-"+m.articleID {
			m.reviews++
			verdict := "supported"
			if strings.Contains(packet.Draft.Segments[0].Text, "twenty four") {
				verdict = "contradicted"
			}
			review.Verdicts = []editorial.Verdict{{TargetID: packet.Targets[0], Verdict: verdict, Reason: "Segment says 124k output tokens; article says 123k."}}
			return editorial.Usage{}, nil
		}
	}
	if reflect.TypeOf(out).Elem().Field(0).Name == "Segment" {
		if strings.Contains(prompt, "Repair") {
			m.repairs++
			return editorial.Usage{}, m.badSegment(out)
		}
		if m.draft.Articles[m.radio].ID == m.articleID {
			m.radio++
			return editorial.Usage{}, m.badSegment(out)
		}
	}
	return m.fakeModel.Generate(ctx, prompt, input, out)
}

func (m *wrongNumberRadio) badSegment(out any) error {
	text := "The model uses one hundred twenty four thousand output tokens per task."
	if m.repairs > 0 {
		text = "Reported output usage is one hundred twenty four thousand tokens per task."
	}
	raw, err := json.Marshal(map[string]any{"segment": content.Segment{
		ID: "radio-" + m.articleID, Title: "Token usage", ArticleIDs: []string{m.articleID},
		Text: text,
	}})
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

func TestRadioNumericalContradictionDoesNotBlockVerifiedNewspaper(t *testing.T) {
	o, base := setup(t, false)
	base.draft.Articles[1].Summary = "The model uses 123k output tokens per task."
	m := &wrongNumberRadio{fakeModel: base, articleID: base.draft.Articles[1].ID}
	o.Model = m
	edition, err := Run(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if err := content.Validate(edition); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(edition.Content.Articles, base.draft.Articles) || len(edition.Content.Segments) != len(base.draft.Articles)-1 || strings.Contains(edition.Content.Script(), "twenty four thousand") {
		t.Fatal("newspaper changed or contradicted narration published")
	}
	if m.reviews != 2 || m.repairs != 1 {
		t.Fatalf("unbounded repair or repeated review: %d/%d", m.reviews, m.repairs)
	}
	var report editorial.GenerationReport
	if err := store.Read(filepath.Join(o.StateDir, o.Date, "generation-report.json"), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Omissions) != 1 || report.Omissions[0].TargetID != "radio-"+m.articleID || report.Omissions[0].Verdict != "contradicted" {
		t.Fatalf("missing factual omission report: %+v", report)
	}
	// Re-publication from checkpoints must neither reroll rejection nor call AI.
	calls := m.calls
	if err := os.Remove(filepath.Join(o.OutputDir, o.Date+".json")); err != nil {
		t.Fatal(err)
	}
	resumed, err := Run(context.Background(), o)
	if err != nil || !reflect.DeepEqual(resumed.Content, edition.Content) || m.calls != calls || m.reviews != 2 || m.repairs != 1 {
		t.Fatalf("resume repeated work or changed published content: %v", err)
	}
}
