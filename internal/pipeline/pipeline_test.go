package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Adi-ty/daywire/internal/collect"
	"github.com/Adi-ty/daywire/internal/content"
	"github.com/Adi-ty/daywire/internal/editorial"
	"github.com/Adi-ty/daywire/internal/store"
)

type fakeModel struct {
	calls                        int
	draft                        content.Draft
	reject                       bool
	forbidDraft                  bool
	repairSucceeds               bool
	repaired                     bool
	article, lesson, quiz, radio int
}

func (f *fakeModel) Generate(_ context.Context, prompt string, input any, out any) (editorial.Usage, error) {
	f.calls++
	switch v := out.(type) {
	case *content.Draft:
		return editorial.Usage{}, fmt.Errorf("monolithic drafting is not allowed")
	case *editorial.Review:
		for _, id := range input.(editorial.VerificationInput).Targets {
			verdict := "supported"
			if f.reject && (!f.repairSucceeds || !f.repaired) && id == "edition" {
				verdict = "insufficient_evidence"
			}
			v.Verdicts = append(v.Verdicts, editorial.Verdict{TargetID: id, Verdict: verdict, Reason: "test verdict"})
		}
	default:
		isRepair := strings.Contains(prompt, "Repair")
		if f.forbidDraft && !isRepair {
			return editorial.Usage{}, fmt.Errorf("unexpected repeated drafting")
		}
		// Writing stage wrappers are private to editorial; inspect their exported
		// field and return the corresponding JSON contract, as a provider would.
		var response any
		switch reflect.TypeOf(out).Elem().Field(0).Name {
		case "Groups":
			groups := [][]string{}
			for _, a := range f.draft.Articles {
				groups = append(groups, a.SourceIDs)
			}
			response = map[string]any{"source_groups": groups}
		case "Article":
			response = map[string]any{"article": f.draft.Articles[f.article]}
			f.article++
		case "Lesson":
			response = map[string]any{"lesson": f.draft.Lessons[f.lesson]}
			f.lesson++
		case "Quiz":
			questions := f.draft.Quiz[:3]
			if f.quiz > 0 {
				questions = f.draft.Quiz[3:]
			}
			response = map[string]any{"quiz": questions}
			f.quiz++
		case "Segment":
			a := f.draft.Articles[f.radio]
			response = map[string]any{"segment": content.Segment{ID: "radio-" + a.ID, Title: a.Headline, Text: a.Summary, ArticleIDs: []string{a.ID}}}
			f.radio++
		case "Title":
			title := f.draft.Title
			if isRepair {
				f.repaired = true
				title = "Repaired " + title
			}
			response = map[string]string{"title": title, "introduction": f.draft.Introduction, "artwork_brief": f.draft.ArtworkBrief}
		default:
			return editorial.Usage{}, fmt.Errorf("unexpected output %T", out)
		}
		raw, _ := json.Marshal(response)
		if err := content.Decode(bytes.NewReader(raw), out); err != nil {
			return editorial.Usage{}, err
		}
	}
	return editorial.Usage{}, nil
}

func setup(t *testing.T, reject bool) (Options, *fakeModel) {
	t.Helper()
	var e content.Edition
	if err := store.Read("../../testdata/demo/2026-10-23.json", &e); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	configs := []collect.Config{{ID: "test", Name: "Test", Kind: "rss", URL: "https://example.com/feed"}}
	path := filepath.Join(root, "sources.json")
	if err := store.JSON(path, configs); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(configs)
	input := editorial.Input{Date: e.Date, PreviousHeadlines: []string{}, Evidence: []content.Evidence{}}
	for _, source := range e.Sources {
		text := "Frozen test evidence"
		input.Evidence = append(input.Evidence, content.Evidence{Source: source, Text: text, Hash: content.Hash([]byte(text)), Kind: "test"})
	}
	state := filepath.Join(root, "state")
	snapshot := Snapshot{Input: input, Coverage: e.Coverage, ConfigHash: content.Hash(raw)}
	if err := store.JSON(filepath.Join(state, e.Date, "input.json"), snapshot); err != nil {
		t.Fatal(err)
	}
	model := &fakeModel{draft: e.Content, reject: reject}
	return Options{Date: e.Date, SourcesPath: path, StateDir: state, OutputDir: filepath.Join(root, "editions"), ModelName: "fake", Model: model}, model
}

func TestResumeDoesNotRepeatCompletedGeneration(t *testing.T) {
	o, model := setup(t, false)
	e, err := Run(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if model.calls != 30 || e.Demo {
		t.Fatalf("calls=%d demo=%v", model.calls, e.Demo)
	}
	path := filepath.Join(o.OutputDir, o.Date+".json")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	if model.calls != 30 {
		t.Fatalf("repeated paid work: %d", model.calls)
	}
	if _, err := Run(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	if model.calls != 30 {
		t.Fatal("existing edition regenerated")
	}
}
func TestRejectedReviewCannotPublish(t *testing.T) {
	o, model := setup(t, true)
	if _, err := Run(context.Background(), o); err == nil {
		t.Fatal("accepted failed review")
	}
	if _, err := os.Stat(filepath.Join(o.OutputDir, o.Date+".json")); !os.IsNotExist(err) {
		t.Fatal("rejected edition written")
	}
	if _, err := Run(context.Background(), o); err == nil {
		t.Fatal("cached rejection accepted")
	}
	if model.calls != 32 {
		t.Fatalf("expected one bounded repair and verification, got %d calls", model.calls)
	}
}
func TestWindowUsesIST(t *testing.T) {
	_, until, err := Window("2026-10-23")
	if err != nil || until.UTC().Format("2006-01-02T15:04") != "2026-10-23T00:30" {
		t.Fatalf("%v %v", until, err)
	}
	if _, _, err := Window("../bad"); err == nil {
		t.Fatal("accepted invalid date")
	}
}

func TestLegacySavedDraftResumesWithoutRegeneration(t *testing.T) {
	o, model := setup(t, false)
	dir := filepath.Join(o.StateDir, o.Date)
	var snapshot Snapshot
	if err := store.Read(filepath.Join(dir, "input.json"), &snapshot); err != nil {
		t.Fatal(err)
	}
	inputBytes, _ := json.Marshal(snapshot)
	schemaBytes, _ := json.Marshal(content.Schema(content.Draft{}))
	legacyKey := content.Hash(append(append(inputBytes, schemaBytes...), []byte(o.ModelName+editorial.PromptHash()+content.Version)...))
	checkpoint := Checkpoint{Key: legacyKey, Draft: model.draft, Usage: editorial.Usage{TotalTokens: 7599}}
	path := filepath.Join(dir, "draft.json")
	if err := store.JSON(path, checkpoint); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	model.forbidDraft = true
	if _, err := Run(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(original) != string(after) {
		t.Fatal("legacy draft was rewritten")
	}
	if model.calls != 15 {
		t.Fatalf("expected only verification; got %d calls", model.calls)
	}
}

func TestRejectedDraftCanPublishOnlyAfterRepairPasses(t *testing.T) {
	o, model := setup(t, true)
	model.repairSucceeds = true
	edition, err := Run(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if !model.repaired || edition.Content.Title == model.draft.Title || model.calls != 32 {
		t.Fatalf("repair not verified: calls=%d", model.calls)
	}
	dir := filepath.Join(o.StateDir, o.Date)
	var original Checkpoint
	if err := store.Read(filepath.Join(dir, "draft.json"), &original); err != nil {
		t.Fatal(err)
	}
	if original.Draft.Title != edition.Content.Title {
		t.Fatal("draft checkpoint did not preserve the accepted correction")
	}
	// Simulate interruption after validation and before atomic public output.
	if err := os.Remove(filepath.Join(o.OutputDir, o.Date+".json")); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	if model.calls != 32 {
		t.Fatal("rerun repeated paid repair")
	}
}
