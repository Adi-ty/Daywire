package editorial

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Adi-ty/daywire/internal/content"
	"github.com/Adi-ty/daywire/internal/store"
)

// CachedModel persists complete, valid stage results, including factual rejections.
// It never caches transport errors, truncated output, or missing review targets.
// The containing run directory is private and included in R2 checkpoint backups.
type CachedModel struct {
	Model     Model
	Directory string
	Identity  string
	Logf      func(string, ...any)
}
type stageCheckpoint struct {
	Key    string `json:"key"`
	Output string `json:"output"`
	Usage  Usage  `json:"usage"`
}

func (m *CachedModel) Generate(ctx context.Context, system string, input, output any) (Usage, error) {
	if err := ctx.Err(); err != nil {
		return Usage{}, err
	}
	keyBytes, err := json.Marshal(struct {
		Identity, Prompt string
		Input            any
		Schema           map[string]any
	}{m.Identity, system, input, dereferenceSchema(output)})
	if err != nil {
		return Usage{}, err
	}
	key := content.Hash(keyBytes)
	path := filepath.Join(m.Directory, key+".json")
	var saved stageCheckpoint
	if err := store.Read(path, &saved); err == nil {
		if saved.Key != key {
			return Usage{}, errors.New("stage checkpoint key mismatch")
		}
		if err := content.Decode(strings.NewReader(saved.Output), output); err != nil {
			return Usage{}, fmt.Errorf("invalid cached stage: %w", err)
		}
		if err := validStage(input, output); err != nil {
			return Usage{}, err
		}
		if m.Logf != nil {
			m.Logf("Reusing completed AI stage %s", key[:12])
		}
		return Usage{}, nil // No tokens consumed by this invocation.
	} else if !os.IsNotExist(err) {
		return Usage{}, err
	}
	used, err := m.Model.Generate(ctx, system, input, output)
	if err != nil {
		return used, err
	}
	if err := validStage(input, output); err != nil {
		return used, err
	}
	raw, err := json.Marshal(output)
	if err != nil {
		return used, err
	}
	if err := ctx.Err(); err != nil {
		return used, err
	}
	return used, store.JSON(path, stageCheckpoint{key, string(raw), used})
}
func validStage(input, output any) error {
	if review, ok := output.(*Review); ok {
		batch, ok := input.(VerificationInput)
		if !ok {
			return errors.New("verification output type mismatch")
		}
		var rejection *Rejection
		if err := Accept(*review, batch.Targets); err != nil && !errors.As(err, &rejection) {
			return err
		}
	}
	return nil
}
