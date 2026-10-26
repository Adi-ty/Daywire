package editorial

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/Adi-ty/daywire/internal/content"
)

// exhaustedTarget distinguishes a completed factual rejection from failures to
// perform a review or correction. Only this error permits editorial omission.
type exhaustedTarget struct {
	id      string
	cause   error
	verdict string
	reason  string
}

func (e *exhaustedTarget) Error() string {
	return fmt.Sprintf("target %s still unsupported after one correction: %v; %s", e.id, e.cause, e.reason)
}
func (e *exhaustedTarget) Unwrap() error { return e.cause }

// settleTarget checks a unit before it can become a premise for another unit.
// One correction is allowed. Rejections are cached, so restarting cannot reroll
// the judge until it approves. Cache keys include the exact current premises.
func settleTarget(ctx context.Context, model Model, input Input, draft *content.Draft, id string) (Usage, error) {
	var total Usage
	add := func(u Usage) {
		total.PromptTokens += u.PromptTokens
		total.CompletionTokens += u.CompletionTokens
		total.TotalTokens += u.TotalTokens
	}
	for attempt := 0; attempt < 2; attempt++ {
		progress(model, "Checking %s against its evidence (attempt %d/2)", id, attempt+1)
		packet := reviewSubset(input, *draft, []string{id})
		review, u, err := reviewTarget(ctx, model, packet)
		add(u)
		if err == nil {
			return total, nil
		}
		var rejection *Rejection
		if !errors.As(err, &rejection) {
			return total, fmt.Errorf("review %s: %w", id, err)
		}
		if attempt == 1 {
			reason := ""
			for _, v := range review.Verdicts {
				if v.TargetID == id {
					reason = v.Reason
				}
			}
			return total, &exhaustedTarget{id: id, cause: err, verdict: rejection.Verdict, reason: reason}
		}
		progress(model, "Correcting %s before generating dependent content", id)
		u, err = correctTarget(ctx, model, input, draft, id, review)
		add(u)
		if err != nil {
			return total, err
		}
	}
	panic("unreachable")
}

func correctTarget(ctx context.Context, model Model, input Input, draft *content.Draft, id string, review Review) (Usage, error) {
	subset := reviewSubset(input, *draft, []string{id})
	relevant := map[string]bool{id: true}
	for _, a := range subset.Draft.Articles {
		relevant[a.ID] = true
	}
	for _, l := range subset.Draft.Lessons {
		relevant[l.ID] = true
	}
	feedback := []Verdict{}
	for _, verdict := range review.Verdicts {
		if relevant[verdict.TargetID] {
			feedback = append(feedback, verdict)
		}
	}
	packet := repairInput{subset, feedback}
	call := func(rules string, out any) (Usage, error) {
		return generateValidated(ctx, model, rules, packet, out)
	}
	changed := errors.New("correction changed target identity or references")
	for i, a := range draft.Articles {
		if a.ID == id {
			var out articleOutput
			u, err := call(repairRules+Prompt("article")+articleDepth(i), &out)
			if err != nil {
				return u, err
			}
			if out.Article.ID != id || !reflect.DeepEqual(out.Article.SourceIDs, a.SourceIDs) {
				return u, changed
			}
			draft.Articles[i] = out.Article
			return u, nil
		}
	}
	for i, l := range draft.Lessons {
		if l.ID == id {
			var out lessonOutput
			u, err := call(repairRules+lessonRepairRules, &out)
			if err != nil {
				return u, err
			}
			if out.Lesson.ID != id || !reflect.DeepEqual(out.Lesson.SourceIDs, l.SourceIDs) || !reflect.DeepEqual(out.Lesson.ArticleIDs, l.ArticleIDs) {
				return u, changed
			}
			draft.Lessons[i] = out.Lesson
			return u, nil
		}
	}
	for i, q := range draft.Quiz {
		if q.ID == id {
			var out questionOutput
			u, err := call(derivedRepairRules+Prompt("quiz")+" Replace the flawed question and its options completely when it is ambiguous. Do not merely add words such as combination, best, only, or fully to salvage overlapping options. Choose a different, directly supported learning objective if necessary. Keep the original question ID and lesson ID.", &out)
			if err != nil {
				return u, err
			}
			if out.Question.ID != id || out.Question.LessonID != q.LessonID {
				return u, changed
			}
			draft.Quiz[i] = out.Question
			return u, nil
		}
	}
	for i, s := range draft.Segments {
		if s.ID == id {
			var out radioOutput
			u, err := call(derivedRepairRules+Prompt("radio"), &out)
			if err != nil {
				return u, err
			}
			if out.Segment.ID != id || !reflect.DeepEqual(out.Segment.ArticleIDs, s.ArticleIDs) {
				return u, changed
			}
			draft.Segments[i] = frameRadio(out.Segment, input.Date, i+1, min(5, len(draft.Articles)))
			return u, nil
		}
	}
	if id == "edition" {
		var out headingOutput
		u, err := call(editionRepairRules, &out)
		if err == nil {
			draft.Title, draft.Introduction, draft.ArtworkBrief = out.Title, out.Introduction, out.ArtworkBrief
		}
		return u, err
	}
	return Usage{}, fmt.Errorf("unknown editorial target %s", id)
}

// Retry only a malformed response once. An evidence rejection is a completed
// judgment, never grounds for asking the same judge again with unchanged facts.
func reviewTarget(ctx context.Context, model Model, packet VerificationInput) (Review, Usage, error) {
	var total Usage
	for attempt := 0; attempt < 2; attempt++ {
		var review Review
		used, err := model.Generate(ctx, verificationPrompt(packet), packet, &review)
		total.PromptTokens += used.PromptTokens
		total.CompletionTokens += used.CompletionTokens
		total.TotalTokens += used.TotalTokens
		if err == nil {
			err = Accept(review, packet.Targets)
		}
		var malformed *CoverageError
		if !errors.As(err, &malformed) || attempt == 1 {
			return review, total, err
		}
		progress(model, "Malformed review for %s; retrying once", packet.Targets[0])
	}
	panic("unreachable")
}
