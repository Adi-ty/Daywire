package editorial

import "github.com/Adi-ty/daywire/internal/content"

// Repair outputs contain only editable content. The same types define the
// provider schema, strict decoder and checkpoint format, so a model cannot
// rename a target or redirect its provenance. apply preserves those original
// links; the corrected text must still pass review against the same premises.
type articleRepairOutput struct {
	Article articleEdit `json:"article"`
}
type articleEdit struct {
	Section     string   `json:"section"`
	Headline    string   `json:"headline"`
	Summary     string   `json:"summary"`
	Paragraphs  []string `json:"paragraphs"`
	Implication string   `json:"implication"`
}

func (edit articleEdit) apply(original content.Article) content.Article {
	original.Section, original.Headline, original.Summary = edit.Section, edit.Headline, edit.Summary
	original.Paragraphs, original.Implication = edit.Paragraphs, edit.Implication
	return original
}

type lessonRepairOutput struct {
	Lesson lessonEdit `json:"lesson"`
}
type lessonEdit struct {
	Title      string   `json:"title"`
	Paragraphs []string `json:"paragraphs"`
	Example    string   `json:"example"`
	Takeaway   string   `json:"takeaway"`
}

func (edit lessonEdit) apply(original content.Lesson) content.Lesson {
	original.Title, original.Paragraphs = edit.Title, edit.Paragraphs
	original.Example, original.Takeaway = edit.Example, edit.Takeaway
	return original
}

type questionRepairOutput struct {
	Question questionEdit `json:"question"`
}
type questionEdit struct {
	Prompt      string   `json:"prompt"`
	Options     []string `json:"options"`
	Answer      int      `json:"answer"`
	Explanation string   `json:"explanation"`
}

func (edit questionEdit) apply(original content.Question) content.Question {
	original.Prompt, original.Options = edit.Prompt, edit.Options
	original.Answer, original.Explanation = edit.Answer, edit.Explanation
	return original
}

type radioRepairOutput struct {
	Segment radioEdit `json:"segment"`
}
type radioEdit struct {
	Title string `json:"title"`
	Text  string `json:"text"`
}

func (edit radioEdit) apply(original content.Segment) content.Segment {
	original.Title, original.Text = edit.Title, edit.Text
	return original
}
