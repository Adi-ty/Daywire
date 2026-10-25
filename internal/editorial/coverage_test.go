package editorial

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

type coverageRecoveryReviewer struct {
	singles   map[string]int
	calls     int
	permanent bool
	reject    string
}

func (m *coverageRecoveryReviewer) Generate(_ context.Context, _ string, in, out any) (Usage, error) {
	m.calls++
	batch := in.(VerificationInput)
	review := out.(*Review)
	if len(batch.Targets) > 1 { // Valid JSON with duplicate IDs is still incomplete.
		id := batch.Targets[0]
		review.Verdicts = []Verdict{{id, "supported", "ok"}, {id, "supported", "ok"}}
	} else {
		id := batch.Targets[0]
		m.singles[id]++
		if m.permanent || m.singles[id] == 1 {
			review.Verdicts = nil
		} else {
			verdict := "supported"
			if id == m.reject {
				verdict = "contradicted"
			}
			review.Verdicts = []Verdict{{id, verdict, "fixture verdict"}}
		}
	}
	return Usage{TotalTokens: 1}, nil
}
func TestMalformedReviewSplitsRetriesAndPreservesRejections(t *testing.T) {
	e, input, _ := repairFixture(t)
	for _, cached := range []bool{false, true} {
		for _, reject := range []string{"", e.Content.Articles[0].ID} {
			m := &coverageRecoveryReviewer{singles: map[string]int{}, reject: reject}
			var model Model = m
			if cached {
				model = &CachedModel{Model: m, Directory: t.TempDir(), Identity: "test"}
			}
			review, usage, err := Verify(context.Background(), model, input, e.Content)
			var rejection *Rejection
			if reject == "" && err != nil {
				t.Fatal(err)
			}
			if reject != "" && (!errors.As(err, &rejection) || rejection.Target != reject) {
				t.Fatalf("lost rejection: %v", err)
			}
			if len(review.Verdicts) != len(e.Content.Targets()) || usage.TotalTokens != m.calls {
				t.Fatal("lost coverage or usage")
			}
			for _, n := range m.singles {
				if n != 2 {
					t.Fatalf("unexpected retry count %d", n)
				}
			}
		}
	}
}
func TestMalformedSingleReviewStopsAfterOneRetry(t *testing.T) {
	e, input, _ := repairFixture(t)
	m := &coverageRecoveryReviewer{singles: map[string]int{}, permanent: true}
	_, _, err := Verify(context.Background(), m, input, e.Content)
	var coverage *CoverageError
	if !errors.As(err, &coverage) {
		t.Fatalf("expected bounded failure, got %v", err)
	}
	for _, n := range m.singles {
		if n != 2 {
			t.Fatalf("unexpected retry count %d", n)
		}
	}
}
func TestReviewSchemaRestrictsIDsAndVerdicts(t *testing.T) {
	batch := VerificationInput{Targets: []string{"story-a", "lesson-b"}}
	s := outputSchema(batch, &Review{})
	fields := s["properties"].(map[string]any)["verdicts"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
	if !reflect.DeepEqual(fields["target_id"].(map[string]any)["enum"], batch.Targets) {
		t.Fatal("unrestricted IDs")
	}
	if !reflect.DeepEqual(fields["verdict"].(map[string]any)["enum"], []string{"supported", "contradicted", "insufficient_evidence"}) {
		t.Fatal("unrestricted verdicts")
	}
}
