package editorial

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Adi-ty/daywire/internal/content"
	"github.com/Adi-ty/daywire/internal/store"
)

func TestVerificationDependencies(t *testing.T) {
	var e content.Edition
	if err := store.Read("../../testdata/demo/2026-10-23.json", &e); err != nil {
		t.Fatal(err)
	}
	input := Input{Date: e.Date, Evidence: []content.Evidence{}}
	for _, s := range e.Sources {
		input.Evidence = append(input.Evidence, content.Evidence{Source: s, Text: "Original evidence for " + s.ID})
	}
	input.Evidence = append(input.Evidence, content.Evidence{Source: content.Source{ID: "irrelevant"}, Text: "Must not be included"})
	q := e.Content.Quiz[0]
	packet := verificationSubset(input, e.Content, []string{q.ID})
	if len(packet.Draft.Quiz) != 1 || len(packet.Draft.Lessons) != 1 || packet.Draft.Lessons[0].ID != q.LessonID || len(packet.Draft.Articles) == 0 || len(packet.Input.Evidence) == 0 {
		t.Fatal("missing quiz dependencies")
	}
	for _, ev := range packet.Input.Evidence {
		if ev.Source.ID == "irrelevant" {
			t.Fatal("included irrelevant evidence")
		}
	}
	for _, a := range packet.Draft.Articles {
		if len(a.Paragraphs) == 0 {
			t.Fatal("article dependency stripped")
		}
	}
	for _, l := range packet.Draft.Lessons {
		for _, id := range l.SourceIDs {
			found := false
			for _, ev := range packet.Input.Evidence {
				if ev.Source.ID == id {
					found = true
				}
			}
			if !found {
				t.Fatal("lost source evidence")
			}
		}
	}
}

func TestCompletedReviewBatchesResumeAfterFailure(t *testing.T) {
	var e content.Edition
	if err := store.Read("../../testdata/demo/2026-10-23.json", &e); err != nil {
		t.Fatal(err)
	}
	input := Input{Date: e.Date, Evidence: []content.Evidence{}}
	for _, s := range e.Sources {
		input.Evidence = append(input.Evidence, content.Evidence{Source: s, Text: strings.Repeat("Original evidence. ", 180)})
	}
	count := len(verificationBatches(input, e.Content))
	if count < 2 {
		t.Fatal("fixture requires multiple batches")
	}
	backend := &batchModel{failAt: 2}
	cache := &CachedModel{Model: backend, Directory: t.TempDir(), Identity: "groq:test"}
	if review, _, err := Verify(context.Background(), cache, input, e.Content); err == nil || len(review.Verdicts) != 0 {
		t.Fatal("incomplete review returned")
	}
	backend.failAt = 0
	if _, _, err := Verify(context.Background(), cache, input, e.Content); err != nil {
		t.Fatal(err)
	}
	if backend.calls != count+1 {
		t.Fatalf("completed work repeated: calls=%d batches=%d", backend.calls, count)
	}
	cache.Identity = "groq:changed-model"
	if _, _, err := Verify(context.Background(), cache, input, e.Content); err != nil {
		t.Fatal(err)
	}
	if backend.calls != 2*count+1 {
		t.Fatal("changed model reused cache")
	}
}

type oversizedBatchModel struct{ calls int }

func (m *oversizedBatchModel) Generate(_ context.Context, _ string, in, out any) (Usage, error) {
	m.calls++
	b := in.(VerificationInput)
	if len(b.Targets) > 1 {
		return Usage{}, &RequestTooLarge{Detail: "provider tokenizer rejected batch"}
	}
	out.(*Review).Verdicts = []Verdict{{b.Targets[0], "supported", "evidence supports it"}}
	return Usage{}, nil
}
func TestSingleTargetPlanAvoidsOversizedBatchesAndRequiresEveryTarget(t *testing.T) {
	d := content.Draft{Articles: []content.Article{{ID: "one"}, {ID: "two"}}}
	m := &oversizedBatchModel{}
	r, _, err := Verify(context.Background(), m, Input{}, d)
	if err != nil || len(r.Verdicts) != 3 || m.calls != 3 {
		t.Fatalf("calls=%d err=%v", m.calls, err)
	}
}

// Opt-in replay of local private inputs; no source text or accepted outputs are
// published, and the transport is never called. Keeps private news out of fixtures.
func TestSavedDraftBudgetReplay(t *testing.T) {
	directory := os.Getenv("DAYWIRE_REPLAY_DIR")
	if directory == "" {
		t.Skip("set DAYWIRE_REPLAY_DIR to a private run directory")
	}
	var snapshot struct {
		Input Input `json:"input"`
	}
	var saved struct {
		Draft content.Draft `json:"draft"`
	}
	raw, err := os.ReadFile(filepath.Join(directory, "input.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(filepath.Join(directory, "draft.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	input := PrepareInput(snapshot.Input)
	batches := verificationBatches(input, saved.Draft)
	for i, b := range batches {
		body, estimate, completion, err := requestBody("openai/gpt-oss-120b", verificationPrompt(b), b, &Review{})
		if err != nil {
			t.Fatalf("batch %d: %v", i+1, err)
		}
		t.Logf("batch %d: targets=%d bytes=%d estimated_input=%d completion=%d", i+1, len(b.Targets), len(body), estimate, completion)
	}
	writer := &stagedWriter{draft: saved.Draft, budget: true}
	var staged content.Draft
	if _, err := GenerateDraft(context.Background(), writer, input, &staged, nil); err != nil {
		t.Fatalf("staged draft budget: %v", err)
	}
	t.Logf("staged writer completed %d bounded calls", writer.calls)
	m := &budgetReviewer{}
	if _, _, err := Verify(context.Background(), m, input, saved.Draft); err != nil {
		t.Fatal(err)
	}
	// Exercise the worst path: every target flagged, every dependent repair sent
	// through the same request builder. No fake outputs are saved or published.
	rejected := Review{}
	for _, id := range saved.Draft.Targets() {
		rejected.Verdicts = append(rejected.Verdicts, Verdict{id, "insufficient_evidence", "A material claim needs correction using its supplied evidence or premises."})
	}
	repairer := &budgetRepairer{t: t}
	repaired, _, err := Repair(context.Background(), repairer, input, saved.Draft, rejected, nil)
	if err != nil {
		t.Fatal(err)
	}
	if repairer.calls != len(saved.Draft.Targets()) {
		t.Fatal("repair replay missed targets")
	}
	if _, _, err := Verify(context.Background(), &budgetReviewer{}, input, repaired); err != nil {
		t.Fatal(err)
	}
	// Budget guard is typed, allowing drafting to choose smaller stages.
	_, _, _, err = requestBody("openai/gpt-oss-120b", Prompt("draft"), input, &content.Draft{})
	var tooLarge *RequestTooLarge
	if !errors.As(err, &tooLarge) {
		t.Fatal("expected draft staging for this regression")
	}
}

type malformedReviewer struct{ calls int }

func (m *malformedReviewer) Generate(_ context.Context, _ string, _ any, out any) (Usage, error) {
	m.calls++
	out.(*Review).Verdicts = []Verdict{}
	return Usage{}, nil
}
func TestIncompleteReviewNeverEntersStageCache(t *testing.T) {
	m := &malformedReviewer{}
	cache := &CachedModel{Model: m, Directory: t.TempDir(), Identity: "groq:test"}
	batch := VerificationInput{Targets: []string{"required"}}
	for i := 0; i < 2; i++ {
		var review Review
		if _, err := cache.Generate(context.Background(), "verify", batch, &review); err == nil {
			t.Fatal("accepted omitted target")
		}
	}
	if m.calls != 2 {
		t.Fatal("malformed output cached")
	}
	entries, err := os.ReadDir(cache.Directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatal("incomplete review written")
	}
}
