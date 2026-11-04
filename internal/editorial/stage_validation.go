package editorial

import (
	"context"
	"errors"
	"fmt"
)

// StageValidationError reports only trusted local contract checks, never model
// claims. It is distinct from an evidence rejection or provider failure.
type StageValidationError struct{ Reason string }

func (e *StageValidationError) Error() string {
	return "invalid generated editorial stage: " + e.Reason
}

func validateLessonReferences(field string, ids []string, allowed map[string]bool) error {
	if len(ids) == 0 {
		return &StageValidationError{"lesson " + field + " must not be empty"}
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if !allowed[id] || seen[id] {
			return &StageValidationError{"lesson " + field + " must contain only unique IDs from the supplied packet's matching article or source records"}
		}
		seen[id] = true
	}
	return nil
}

// A structural correction is allowed once, before any dependent generation.
// Evidence review remains mandatory; satisfying the shape does not approve facts.
func generateValidated(ctx context.Context, model Model, system string, input, output any) (Usage, error) {
	var total Usage
	for attempt := 0; attempt < 2; attempt++ {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		switch out := output.(type) {
		case *selectionOutput:
			*out = selectionOutput{}
		case *rankingOutput:
			*out = rankingOutput{}
		case *lessonOutput:
			*out = lessonOutput{}
		case *radioOutput:
			*out = radioOutput{}
		case *articleRepairOutput:
			*out = articleRepairOutput{}
		case *lessonRepairOutput:
			*out = lessonRepairOutput{}
		case *questionRepairOutput:
			*out = questionRepairOutput{}
		case *radioRepairOutput:
			*out = radioRepairOutput{}
		}
		used, err := model.Generate(ctx, system, input, output)
		total.PromptTokens += used.PromptTokens
		total.CompletionTokens += used.CompletionTokens
		total.TotalTokens += used.TotalTokens
		if err == nil {
			err = validStage(input, output)
		}
		var invalid *StageValidationError
		if !errors.As(err, &invalid) {
			return total, err
		}
		if attempt == 1 {
			return total, fmt.Errorf("stage structural correction exhausted: %w", err)
		}
		progress(model, "Stage failed local validation (%s); correcting once before dependent stages", invalid.Reason)
		switch output.(type) {
		case *selectionOutput, *rankingOutput:
			system += " Local contract correction: " + invalid.Reason + ". Use only supplied IDs, once each; respect the requested count and return the complete JSON result."
			continue
		}
		switch output.(type) {
		case *lessonOutput:
			system += " Local contract correction: " + invalid.Reason + ". Return the complete lesson with nonempty title, paragraphs, example and takeaway. Copy article_ids only from packet.draft.articles[].id and source_ids only from packet.input.evidence[].source.id; these are different ID types. Use each cited ID once and never invent facts merely to fill a field."
			continue
		case *radioOutput, *radioRepairOutput:
			system += " Local contract correction: " + invalid.Reason + ". Return only the story body using its original premises and the exact supplied output schema."
			continue
		}
		system += " Local contract correction: " + invalid.Reason + ". Return the complete lesson. Keep title, paragraphs, example and takeaway nonempty and concise. Replace unsupported examples with a plain-language illustration supported by the supplied evidence; never invent facts merely to fill a field."
	}
	return total, errors.New("stage correction budget exhausted")
}
