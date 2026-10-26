package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Adi-ty/daywire/internal/collect"
	"github.com/Adi-ty/daywire/internal/content"
	"github.com/Adi-ty/daywire/internal/editorial"
	"github.com/Adi-ty/daywire/internal/fetch"
	"github.com/Adi-ty/daywire/internal/store"
)

type Options struct {
	Date        string
	SourcesPath string
	StateDir    string
	OutputDir   string
	ModelName   string
	Model       editorial.Model
	Logf        func(string, ...any)
}
type Snapshot struct {
	Input      editorial.Input  `json:"input"`
	Coverage   content.Coverage `json:"coverage"`
	ConfigHash string           `json:"config_hash"`
}
type Checkpoint struct {
	Key   string          `json:"key"`
	Draft content.Draft   `json:"draft"`
	Usage editorial.Usage `json:"usage"`
}
type Verification struct {
	Key    string           `json:"key"`
	Review editorial.Review `json:"review"`
	Usage  editorial.Usage  `json:"usage"`
}

func Window(date string) (time.Time, time.Time, error) {
	zone, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	day, err := time.ParseInLocation("2006-01-02", date, zone)
	if err != nil || day.Format("2006-01-02") != date {
		return time.Time{}, time.Time{}, errors.New("date must be YYYY-MM-DD")
	}
	until := day.Add(6 * time.Hour)
	return until.Add(-24 * time.Hour), until, nil
}

func Run(ctx context.Context, o Options) (content.Edition, error) {
	var edition content.Edition
	from, until, err := Window(o.Date)
	if err != nil {
		return edition, err
	}
	if until.After(time.Now()) {
		return edition, errors.New("edition cutoff is in the future; generate after 06:00 Asia/Kolkata")
	}
	output := filepath.Join(o.OutputDir, o.Date+".json")
	if err := store.Read(output, &edition); err == nil {
		if edition.Date != o.Date {
			return edition, errors.New("existing edition date does not match requested date")
		}
		if edition.Demo {
			return edition, errors.New("demo content cannot be reused as a live edition")
		}
		return edition, content.Validate(edition)
	} else if !os.IsNotExist(err) {
		return edition, err
	}
	if o.Model == nil {
		return edition, errors.New("generation model is required")
	}
	var configs []collect.Config
	if err := store.Read(o.SourcesPath, &configs); err != nil {
		return edition, err
	}
	if len(configs) == 0 || len(configs) > 30 {
		return edition, errors.New("configure between 1 and 30 sources")
	}
	configIDs := map[string]bool{}
	for _, cfg := range configs {
		if cfg.ID == "" || configIDs[cfg.ID] || cfg.Name == "" || !content.WebURL(cfg.URL) {
			return edition, errors.New("invalid source configuration")
		}
		configIDs[cfg.ID] = true
	}
	configBytes, _ := json.Marshal(configs)
	configHash := content.Hash(configBytes)
	dir := filepath.Join(o.StateDir, o.Date)
	model := &editorial.CachedModel{Model: o.Model, Directory: filepath.Join(dir, "stages"), Identity: "groq:" + o.ModelName + ":budget-v1", Logf: o.Logf}
	var snapshot Snapshot
	if err := store.Read(filepath.Join(dir, "input.json"), &snapshot); err != nil {
		if !os.IsNotExist(err) {
			return edition, err
		}
		if o.Logf != nil {
			o.Logf("Collecting %d sources for %s to %s", len(configs), from.Format(time.RFC3339), until.Format(time.RFC3339))
		}
		collector := collect.Collector{Client: fetch.NewClient()}
		report, err := collector.Run(ctx, configs, from, until)
		if err != nil {
			return edition, err
		}
		previous, seen, err := History(o.OutputDir, o.Date)
		if err != nil {
			return edition, err
		}
		fresh := []collect.Candidate{}
		for _, candidate := range report.Candidates {
			if !seen[candidate.Source.URL] {
				fresh = append(fresh, candidate)
			}
		}
		evidence, failed, err := collector.Evidence(ctx, fresh)
		if err != nil {
			return edition, err
		}
		if o.Logf != nil {
			o.Logf("Collection: %d candidates, %d evidence excerpts, %d/%d unavailable feeds, %d unavailable article pages", len(report.Candidates), len(evidence), len(report.Unavailable), len(configs), len(failed))
		}
		if len(evidence) < 2 {
			if len(report.Unavailable) == len(configs) {
				return edition, fmt.Errorf("all %d configured feeds unavailable; check network/DNS and source endpoints; no edition generated", len(configs))
			}
			return edition, errors.New("fewer than two usable evidence documents; no edition generated")
		}
		unavailable := append(report.Unavailable, failed...)
		status := "complete"
		if len(unavailable) > 0 {
			status = "partial"
		}
		snapshot = Snapshot{Input: editorial.Input{Date: o.Date, Evidence: evidence, PreviousHeadlines: previous}, ConfigHash: configHash, Coverage: content.Coverage{From: from, Until: until, Status: status, UnavailableSources: unavailable, CandidateCount: len(report.Candidates), EvidenceCount: len(evidence)}}
		if err := store.JSON(filepath.Join(dir, "input.json"), snapshot); err != nil {
			return edition, err
		}
	} else if snapshot.ConfigHash != configHash || snapshot.Input.Date != o.Date || !snapshot.Coverage.Until.Equal(until) {
		return edition, errors.New("saved input differs from configuration; use a different state directory")
	}
	modelInput := editorial.PrepareInput(snapshot.Input)
	if o.Logf != nil {
		o.Logf("Collected %d candidates, saved %d evidence excerpts; using %d in the briefing", snapshot.Coverage.CandidateCount, len(snapshot.Input.Evidence), len(modelInput.Evidence))
	}
	inputBytes, _ := json.Marshal(snapshot)
	schemaBytes, _ := json.Marshal(content.Schema(content.Draft{}))
	legacyKey := content.Hash(append(append(inputBytes, schemaBytes...), []byte(o.ModelName+editorial.PromptHash()+content.Version)...))
	key := content.Hash([]byte(legacyKey + ":budgeted-draft-v1"))
	sources := []content.Source{}
	for _, e := range snapshot.Input.Evidence {
		if content.Hash([]byte(e.Text)) != e.Hash {
			return edition, errors.New("evidence hash mismatch")
		}
		sources = append(sources, e.Source)
	}
	var checkpoint Checkpoint
	if err := store.Read(filepath.Join(dir, "draft.json"), &checkpoint); err != nil || (checkpoint.Key != key && checkpoint.Key != legacyKey) {
		if err != nil && !os.IsNotExist(err) {
			return edition, err
		}
		var draft content.Draft
		if o.Logf != nil {
			o.Logf("Drafting edition with Groq")
		}
		usage, report, err := editorial.GenerateDraftWithReport(ctx, model, modelInput, &draft, o.Logf)
		if reportErr := store.JSON(filepath.Join(dir, "generation-report.json"), report); reportErr != nil {
			return edition, fmt.Errorf("save generation report: %w", reportErr)
		}
		if err != nil {
			return edition, err
		}
		checkpoint = Checkpoint{Key: key, Draft: draft, Usage: usage}
		if err := content.ValidateDraft(checkpoint.Draft, sources); err != nil {
			return edition, fmt.Errorf("draft validation: %w", err)
		}
		if err := store.JSON(filepath.Join(dir, "draft.json"), checkpoint); err != nil {
			return edition, err
		}
	} else if o.Logf != nil {
		o.Logf("Reusing saved draft; resuming verification")
	}
	if err := content.ValidateDraft(checkpoint.Draft, sources); err != nil {
		return edition, fmt.Errorf("draft validation: %w", err)
	}
	draftBytes, _ := json.Marshal(checkpoint.Draft)
	reviewKey := content.Hash(append([]byte(checkpoint.Key+editorial.ReviewHash()), draftBytes...))
	var verification Verification
	if err := store.Read(filepath.Join(dir, "review.json"), &verification); err != nil || verification.Key != reviewKey {
		if err != nil && !os.IsNotExist(err) {
			return edition, err
		}
		if o.Logf != nil {
			o.Logf("Verifying all editorial targets against evidence")
		}
		review, usage, verifyErr := editorial.Verify(ctx, model, modelInput, checkpoint.Draft)
		if verifyErr != nil && len(review.Verdicts) == 0 {
			return edition, verifyErr
		}
		verification = Verification{Key: reviewKey, Review: review, Usage: usage}
		if err := store.JSON(filepath.Join(dir, "review.json"), verification); err != nil {
			return edition, err
		}
	}
	if err := editorial.Accept(verification.Review, checkpoint.Draft.Targets()); err != nil {
		var rejection *editorial.Rejection
		if !errors.As(err, &rejection) {
			return edition, err
		}
		repaired, err := repairOnce(ctx, model, modelInput, checkpoint.Draft, verification.Review, dir, "groq:"+o.ModelName, sources, o.Logf)
		if err != nil {
			return edition, err
		}
		checkpoint.Draft = repaired
	}
	usedSources := map[string]bool{}
	for _, a := range checkpoint.Draft.Articles {
		for _, id := range a.SourceIDs {
			usedSources[id] = true
		}
	}
	for _, l := range checkpoint.Draft.Lessons {
		for _, id := range l.SourceIDs {
			usedSources[id] = true
		}
	}
	publicSources := []content.Source{}
	for _, s := range sources {
		if usedSources[s.ID] {
			publicSources = append(publicSources, s)
		}
	}
	edition = content.Edition{SchemaVersion: content.Version, Date: o.Date, Timezone: "Asia/Kolkata", Revision: 1, GeneratedAt: time.Now().UTC(), Coverage: snapshot.Coverage, Content: checkpoint.Draft, Sources: publicSources, Radio: content.Radio{Status: "pending", ScriptHash: content.Hash([]byte(checkpoint.Draft.Script()))}, Artwork: content.Artwork{Status: "pending", Alt: ""}}
	if err := content.Validate(edition); err != nil {
		return edition, err
	}
	if err := ctx.Err(); err != nil {
		return edition, err
	}
	return edition, store.JSON(output, edition)
}

func History(dir, date string) ([]string, map[string]bool, error) {
	headlines := []string{}
	seen := map[string]bool{}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return headlines, seen, nil
	}
	if err != nil {
		return nil, nil, err
	}
	day, err := time.Parse("2006-01-02", date)
	if err != nil {
		return nil, nil, err
	}
	// Edition filenames sort chronologically. Put the most recent coverage
	// first so a bounded novelty prompt does not prefer week-old headlines.
	for index := len(entries) - 1; index >= 0; index-- {
		entry := entries[index]
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		var e content.Edition
		if err := store.Read(filepath.Join(dir, entry.Name()), &e); err != nil {
			return nil, nil, err
		}
		if err := content.Validate(e); err != nil {
			return nil, nil, err
		}
		d, _ := time.Parse("2006-01-02", e.Date)
		age := day.Sub(d)
		if e.Demo || age <= 0 || age > 7*24*time.Hour {
			continue
		}
		for _, a := range e.Content.Articles {
			headlines = append(headlines, a.Headline)
		}
		for _, s := range e.Sources {
			seen[s.URL] = true
		}
	}
	return headlines, seen, nil
}
