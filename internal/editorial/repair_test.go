package editorial

import (
	"context"
	"reflect"
	"testing"

	"github.com/Adi-ty/daywire/internal/content"
	"github.com/Adi-ty/daywire/internal/store"
)

type repairModel struct {
	ids []string
}

func (m *repairModel) Generate(_ context.Context, _ string, in, out any) (Usage, error) {
	packet := in.(repairInput).Packet
	m.ids = append(m.ids, packet.Targets[0])
	switch v := out.(type) {
	case *lessonRepairOutput:
		copyRepairFields(packet.Draft.Lessons[0], &v.Lesson)
		v.Lesson.Example = "Corrected example grounded in evidence"
	case *questionRepairOutput:
		copyRepairFields(packet.Draft.Quiz[0], &v.Question)
		v.Question.Explanation = "Corrected explanation following the updated lesson"
	case *radioRepairOutput:
		copyRepairFields(packet.Draft.Segments[0], &v.Segment)
		v.Segment.Text = "Corrected radio wording grounded in evidence"
	}
	return Usage{TotalTokens: 10}, nil
}
func repairFixture(t *testing.T) (content.Edition, Input, Review) {
	t.Helper()
	var e content.Edition
	if err := store.Read("../../testdata/demo/2026-10-23.json", &e); err != nil {
		t.Fatal(err)
	}
	input := Input{Date: e.Date, Evidence: []content.Evidence{}}
	for _, s := range e.Sources {
		input.Evidence = append(input.Evidence, content.Evidence{Source: s, Text: "Frozen evidence"})
	}
	review := Review{Verdicts: []Verdict{}}
	for _, id := range e.Content.Targets() {
		verdict := "supported"
		if id == e.Content.Lessons[0].ID || id == e.Content.Segments[0].ID {
			verdict = "insufficient_evidence"
		}
		review.Verdicts = append(review.Verdicts, Verdict{id, verdict, "test evidence judgment"})
	}
	return e, input, review
}
func TestRepairUpdatesDependenciesAndPreservesOriginal(t *testing.T) {
	e, input, review := repairFixture(t)
	before := e.Content.Lessons[0].Example
	model := &repairModel{}
	repaired, usage, err := Repair(context.Background(), model, input, e.Content, review, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{e.Content.Lessons[0].ID}
	for _, q := range e.Content.Quiz {
		if q.LessonID == e.Content.Lessons[0].ID {
			want = append(want, q.ID)
		}
	}
	want = append(want, e.Content.Segments[0].ID)
	if !reflect.DeepEqual(model.ids, want) {
		t.Fatalf("repaired %v; want %v", model.ids, want)
	}
	if e.Content.Lessons[0].Example != before || repaired.Lessons[0].Example == before {
		t.Fatal("original mutated or correction missing")
	}
	if !reflect.DeepEqual(repaired.Articles, e.Content.Articles) {
		t.Fatal("unaffected articles rewritten")
	}
	if usage.TotalTokens != len(want)*10 {
		t.Fatal("lost usage")
	}
	if err := content.ValidateDraft(repaired, e.Sources); err != nil {
		t.Fatal(err)
	}
}
func TestRepairOwnsIdentityAndRefusesIncompleteReview(t *testing.T) {
	e, input, review := repairFixture(t)
	repaired, _, err := Repair(context.Background(), &repairModel{}, input, e.Content, review, nil)
	if err != nil || repaired.Lessons[0].ID != e.Content.Lessons[0].ID {
		t.Fatal("repair did not preserve application-owned identity", err)
	}
	review.Verdicts = review.Verdicts[:1]
	model := &repairModel{}
	if _, _, err := Repair(context.Background(), model, input, e.Content, review, nil); err == nil || len(model.ids) > 0 {
		t.Fatal("incomplete review started repair")
	}
}
