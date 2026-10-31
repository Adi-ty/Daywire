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
	}{m.Identity, system, input, outputSchema(input, output)})
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
		if err := validStage(input, output); err == nil {
			if m.Logf != nil {
				m.Logf("Reusing completed AI stage %s", key[:12])
			}
			return Usage{}, nil
		} else {
			var invalid *StageValidationError
			if !errors.As(err, &invalid) {
				return Usage{}, err
			}
			// Older versions cached structurally invalid lessons. Regenerate
			// this stage; never use it as a premise or edit saved facts by hand.
			if m.Logf != nil {
				m.Logf("Regenerating invalid cached editorial stage: %s", invalid.Reason)
			}
			switch out := output.(type) {
			case *lessonOutput:
				*out = lessonOutput{}
			case *radioOutput:
				*out = radioOutput{}
			case *lessonRepairOutput:
				*out = lessonRepairOutput{}
			case *radioRepairOutput:
				*out = radioRepairOutput{}
			}
		}
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
	if out, ok := output.(*selectionOutput); ok {
		if packet, ok := input.(selectionPacket); ok {
			return validateSelection(packet, *out)
		}
	}
	if out, ok := output.(*rankingOutput); ok {
		packet, ok := input.([]articlePreview)
		if !ok {
			return errors.New("ranking input type mismatch")
		}
		wanted := map[string]bool{}
		for _, a := range packet {
			wanted[a.ID] = true
		}
		if len(out.ArticleIDs) != len(packet) {
			return &StageValidationError{"ranking must include every supplied article exactly once"}
		}
		for _, id := range out.ArticleIDs {
			if !wanted[id] {
				return &StageValidationError{"ranking contains an unknown or repeated article ID"}
			}
			delete(wanted, id)
		}
	}
	if out, ok := output.(*radioOutput); ok {
		if err := radioBodyError(out.Segment.Text); err != nil {
			return err
		}
	}
	if out, ok := output.(*lessonOutput); ok {
		if err := content.ValidateLessonText(out.Lesson); err != nil {
			return &StageValidationError{err.Error()}
		}
	}
	if out, ok := output.(*radioRepairOutput); ok {
		if err := radioBodyError(out.Segment.Text); err != nil {
			return err
		}
	}
	if out, ok := output.(*lessonRepairOutput); ok {
		if err := content.ValidateLessonText(out.Lesson.apply(content.Lesson{})); err != nil {
			return &StageValidationError{err.Error()}
		}
	}
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
