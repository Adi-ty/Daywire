package evaluation

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Adi-ty/daywire/internal/content"
	"github.com/Adi-ty/daywire/internal/editorial"
	"github.com/Adi-ty/daywire/internal/store"
)

type unavailableModel struct{}

func (unavailableModel) Generate(context.Context, string, any, any) (editorial.Usage, error) {
	return editorial.Usage{}, errors.New("provider unavailable")
}
func TestProviderFailureIsNotASuccessfulRejection(t *testing.T) {
	var cases []Case
	if err := store.Read("../../evals/development.json", &cases); err != nil {
		t.Fatal(err)
	}
	report := Run(context.Background(), unavailableModel{}, "test", cases[1:2])
	if report.Errors != 1 || report.Cases[0].Match {
		t.Fatalf("provider outage counted as correct judgment: %+v", report)
	}
}

type waitingModel struct{}

func (waitingModel) Generate(ctx context.Context, _ string, _ any, _ any) (editorial.Usage, error) {
	<-ctx.Done()
	return editorial.Usage{}, ctx.Err()
}
func TestCaseDeadlineAndProgress(t *testing.T) {
	var cases []Case
	if err := store.Read("../../evals/development.json", &cases); err != nil {
		t.Fatal(err)
	}
	events := 0
	report := RunWithOptions(context.Background(), waitingModel{}, "test", cases[:2], Options{CaseTimeout: time.Millisecond, Progress: func(index, total int, id string, result *Result) { events++ }})
	if len(report.Cases) != 2 || report.Errors != 2 || events != 4 {
		t.Fatalf("report=%+v events=%d", report, events)
	}
}
func TestCancellationStopsStartingNewCases(t *testing.T) {
	var cases []Case
	if err := store.Read("../../evals/development.json", &cases); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	report := RunWithOptions(ctx, waitingModel{}, "test", cases, Options{Progress: func(index, total int, id string, result *Result) {
		if result == nil {
			cancel()
		}
	}})
	if len(report.Cases) != 1 || report.Errors != 1 {
		t.Fatalf("cancellation did not stop run: %+v", report)
	}
}

// These must reach the AI judge: neither positive nor negative cases should
// be rejected merely because the fixture is malformed or its hashes are stale.
func TestRadioWritingFixturesAreValid(t *testing.T) {
	checkEditorialFixtures(t, "../../evals/radio-writing.json")
	checkEditorialFixtures(t, "../../evals/editorial-regressions.json")
	checkEditorialFixtures(t, "../../evals/quiz-regressions.json")
}
func checkEditorialFixtures(t *testing.T, path string) {
	t.Helper()
	var cases []Case
	if err := store.Read(path, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.ID, func(t *testing.T) {
			sources := []content.Source{}
			for _, e := range c.Input.Evidence {
				if content.Hash([]byte(e.Text)) != e.Hash {
					t.Fatal("stale evidence hash")
				}
				sources = append(sources, e.Source)
			}
			if err := content.ValidateDraft(c.Draft, sources); err != nil {
				t.Fatal(err)
			}
		})
	}
}
