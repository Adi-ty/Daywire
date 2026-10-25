package editorial

import (
	"strings"

	"github.com/Adi-ty/daywire/internal/content"
)

// PrepareInput bounds the writer's working set. The complete retrieval snapshot
// remains on disk; shortened excerpts are identified and rehashed, never summarized.
func PrepareInput(input Input) Input {
	result := Input{Date: input.Date, Evidence: []content.Evidence{}, PreviousHeadlines: []string{}}
	for i, e := range input.Evidence {
		if i == 6 {
			break
		}
		shortened := excerpt(e.Text, 1600)
		if shortened != e.Text {
			e.Text = shortened
			e.Hash = content.Hash([]byte(shortened))
			e.Kind += "_truncated"
		}
		result.Evidence = append(result.Evidence, e)
	}
	for i, headline := range input.PreviousHeadlines {
		if i == 30 {
			break
		}
		result.PreviousHeadlines = append(result.PreviousHeadlines, excerpt(headline, 200))
	}
	return result
}
func excerpt(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	prefix := string(runes[:limit])
	// Prefer a complete sentence near the boundary. Never skip forward to unseen text.
	if end := strings.LastIndex(prefix, ". "); end >= len(prefix)/2 {
		return prefix[:end+1]
	}
	if end := strings.LastIndex(prefix, " "); end > 0 {
		return prefix[:end]
	}
	return prefix
}

type VerificationInput struct {
	Input   Input         `json:"input"`
	Draft   content.Draft `json:"draft"`
	Targets []string      `json:"required_targets"`
}

// The edition checks composition against article summaries; every article is
// separately checked against its original evidence. Keep that check isolated so
// it never pulls the entire source corpus into one indivisible request.
// Other targets retain their transitive evidence without further truncation.
func verificationBatches(input Input, draft content.Draft) []VerificationInput {
	batches := []VerificationInput{}
	// Source-grounded claims first; transformations then check only their direct
	// premises. All four groups are mandatory, regardless of earlier verdicts.
	for _, kind := range []string{"source", "quiz", "radio", "edition"} {
		targets := []string{}
		for _, id := range draft.Targets() {
			if reviewKind(draft, id) != kind {
				continue
			}
			candidate := append(append([]string{}, targets...), id)
			batch := reviewSubset(input, draft, candidate)
			_, _, _, err := requestBody("openai/gpt-oss-120b", verificationPrompt(batch), batch, &Review{})
			// Input fit alone does not bound the reasoning/output needed to
			// judge every claim. Use one target per completion so cache identity and judgments
			// never depend on unrelated neighboring targets.
			if (err != nil || len(candidate) > 1) && len(targets) > 0 {
				batches = append(batches, reviewSubset(input, draft, targets))
				targets = []string{id}
			} else {
				targets = candidate
			}
		}
		if len(targets) > 0 {
			batches = append(batches, reviewSubset(input, draft, targets))
		}
	}
	return batches
}

func reviewKind(draft content.Draft, id string) string {
	if id == "edition" {
		return "edition"
	}
	for _, q := range draft.Quiz {
		if q.ID == id {
			return "quiz"
		}
	}
	for _, s := range draft.Segments {
		if s.ID == id {
			return "radio"
		}
	}
	return "source"
}

// A quiz must follow its lesson; narration must follow its articles. The
// corresponding premises must ALSO pass source review before publication. This
// avoids resending the entire transitive source graph for each transformation.
func reviewSubset(input Input, draft content.Draft, targets []string) VerificationInput {
	packet := verificationSubset(input, draft, targets)
	if len(targets) == 0 {
		return packet
	}
	switch reviewKind(draft, targets[0]) {
	case "quiz":
		packet.Input.Evidence = []content.Evidence{}
		packet.Draft.Articles = []content.Article{}
	case "radio":
		packet.Input.Evidence = []content.Evidence{}
	}
	return packet
}

func verificationSubset(input Input, draft content.Draft, targets []string) VerificationInput {
	wanted, articleIDs, lessonIDs, sourceIDs := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, id := range targets {
		wanted[id] = true
	}
	d := content.Draft{Articles: []content.Article{}, Lessons: []content.Lesson{}, Quiz: []content.Question{}, Segments: []content.Segment{}}
	subset := Input{Date: input.Date, Evidence: []content.Evidence{}, PreviousHeadlines: []string{}}
	if wanted["edition"] {
		d.Title, d.Introduction, d.ArtworkBrief = draft.Title, draft.Introduction, draft.ArtworkBrief
		subset.PreviousHeadlines = input.PreviousHeadlines
	}
	for _, q := range draft.Quiz {
		if wanted[q.ID] {
			d.Quiz = append(d.Quiz, q)
			lessonIDs[q.LessonID] = true
		}
	}
	for _, s := range draft.Segments {
		if wanted[s.ID] {
			d.Segments = append(d.Segments, s)
			for _, id := range s.ArticleIDs {
				articleIDs[id] = true
			}
		}
	}
	for _, l := range draft.Lessons {
		if wanted[l.ID] || lessonIDs[l.ID] {
			d.Lessons = append(d.Lessons, l)
			for _, id := range l.ArticleIDs {
				articleIDs[id] = true
			}
			for _, id := range l.SourceIDs {
				sourceIDs[id] = true
			}
		}
	}
	for _, a := range draft.Articles {
		if wanted[a.ID] || articleIDs[a.ID] || wanted["edition"] {
			if wanted["edition"] && !wanted[a.ID] && !articleIDs[a.ID] {
				// Do not treat this summary as independently established evidence:
				// Verify requires a separate successful source review of every article.
				a.Paragraphs = []string{}
				a.Implication = ""
			} else {
				for _, id := range a.SourceIDs {
					sourceIDs[id] = true
				}
			}
			d.Articles = append(d.Articles, a)
		}
	}
	for _, e := range input.Evidence {
		if sourceIDs[e.Source.ID] {
			subset.Evidence = append(subset.Evidence, e)
		}
	}
	return VerificationInput{subset, d, targets}
}
