package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Adi-ty/daywire/internal/content"
	"github.com/Adi-ty/daywire/internal/editorial"
	"github.com/Adi-ty/daywire/internal/store"
)

// The original draft/review remain intact. A completed repair and its review
// have separate checkpoints, so reruns cannot start an unbounded repair loop.
func repairOnce(ctx context.Context, model editorial.Model, input editorial.Input, draft content.Draft, review editorial.Review, dir, identity string, sources []content.Source, logf func(string, ...any)) (content.Draft, error) {
	raw, err := json.Marshal(struct {
		Input                     editorial.Input
		Draft                     content.Draft
		Review                    editorial.Review
		Identity, Policy, Prompts string
	}{input, draft, review, identity, editorial.RepairHash(), editorial.PromptHash()})
	if err != nil {
		return content.Draft{}, err
	}
	key := content.Hash(raw)
	var repaired Checkpoint
	path := filepath.Join(dir, "repair.json")
	if err := store.Read(path, &repaired); err != nil || repaired.Key != key {
		if err != nil && !os.IsNotExist(err) {
			return content.Draft{}, err
		}
		if logf != nil {
			logf("Verification rejected content; starting one evidence-based repair pass")
		}
		candidate, usage, err := editorial.Repair(ctx, model, input, draft, review, logf)
		if err != nil {
			return content.Draft{}, err
		}
		if err := content.ValidateDraft(candidate, sources); err != nil {
			return content.Draft{}, fmt.Errorf("repaired draft validation: %w", err)
		}
		repaired = Checkpoint{Key: key, Draft: candidate, Usage: usage}
		if err := store.JSON(path, repaired); err != nil {
			return content.Draft{}, err
		}
	} else if logf != nil {
		logf("Reusing saved repair; no second repair pass")
	}
	if err := content.ValidateDraft(repaired.Draft, sources); err != nil {
		return content.Draft{}, err
	}
	body, _ := json.Marshal(repaired.Draft)
	reviewKey := content.Hash(append([]byte(key+editorial.ReviewHash()), body...))
	path = filepath.Join(dir, "repair-review.json")
	var verified Verification
	if err := store.Read(path, &verified); err != nil || verified.Key != reviewKey {
		if err != nil && !os.IsNotExist(err) {
			return content.Draft{}, err
		}
		if logf != nil {
			logf("Verifying repaired edition; reusing unchanged review batches when available")
		}
		result, usage, verifyErr := editorial.Verify(ctx, model, input, repaired.Draft)
		if verifyErr != nil && len(result.Verdicts) == 0 {
			return content.Draft{}, verifyErr
		}
		verified = Verification{Key: reviewKey, Review: result, Usage: usage}
		if err := store.JSON(path, verified); err != nil {
			return content.Draft{}, err
		}
	}
	if err := editorial.Accept(verified.Review, repaired.Draft.Targets()); err != nil {
		var rejection *editorial.Rejection
		if errors.As(err, &rejection) {
			return content.Draft{}, fmt.Errorf("repair pass exhausted; publication stopped: %w", err)
		}
		return content.Draft{}, err
	}
	return repaired.Draft, nil
}
