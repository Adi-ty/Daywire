package editorial

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Adi-ty/daywire/internal/content"
)

type stageCall func(label, prompt string, in, out any) error

// Selection uses small metadata packets, never the full evidence corpus. A
// two-level shortlist gives later feeds a chance without growing any request.
type storyPreview struct {
	ID        string     `json:"id"`
	Title     string     `json:"title"`
	Publisher string     `json:"publisher"`
	Published *time.Time `json:"published_at"`
	Kind      string     `json:"kind"`
	Excerpt   string     `json:"excerpt"`
}
type selectionPacket struct {
	Date     string         `json:"date"`
	Stories  []storyPreview `json:"stories"`
	Previous []string       `json:"previous_headlines"`
	Limit    int            `json:"limit"`
}

const selectionRules = `Select distinct useful developments for software engineers. Return groups of exact source IDs; combine coverage of the same event, primary source first, at most two sources per group. Do not repeat a source. Rank concrete developer impact, evidence quality, technical insight and novelty relative to previous headlines. Include useful engineering explanations and tools alongside consequential AI and security developments; avoid letting one prolific publisher or repeated advisories crowd out equally useful stories. Preview or vendor claims must remain attributed. Old discoveries are reading recommendations, not new announcements. Choose fewer stories if the material is weak; never fill a quota with hype. The writer receives longer evidence separately. `

func previews(input Input, limit int) selectionPacket {
	p := selectionPacket{Date: input.Date, Limit: limit, Stories: []storyPreview{}, Previous: []string{}}
	for _, e := range input.Evidence {
		p.Stories = append(p.Stories, storyPreview{e.Source.ID, excerpt(e.Source.Title, 140), excerpt(e.Source.Name, 50), e.Source.PublishedAt, e.Kind, excerpt(e.Text, 320)})
	}
	for _, h := range input.PreviousHeadlines[:min(12, len(input.PreviousHeadlines))] {
		p.Previous = append(p.Previous, excerpt(h, 100))
	}
	return p
}

func selectStories(input Input, call stageCall) (selectionOutput, error) {
	pool := input
	companions := map[string][]string{}
	if len(input.Evidence) > 8 {
		pool.Evidence = []content.Evidence{}
		for start := 0; start < len(input.Evidence); start += 8 {
			batch := input
			batch.Evidence = input.Evidence[start:min(start+8, len(input.Evidence))]
			var out selectionOutput
			if err := call(fmt.Sprintf("shortlist %d/%d", start/8+1, (len(input.Evidence)+7)/8), selectionRules+"Nominate up to four stories from this batch, or an empty list if none merit coverage.", previews(batch, 4), &out); err != nil {
				return out, err
			}
			// Send one preview per nomination, retaining its corroborating source
			// for writing. Final selection can merge nominations across batches.
			for _, group := range out.Groups {
				companions[group[0]] = group
				for _, e := range batch.Evidence {
					if e.Source.ID == group[0] {
						pool.Evidence = append(pool.Evidence, e)
						break
					}
				}
			}
		}
	}
	if len(pool.Evidence) == 0 {
		return selectionOutput{}, errors.New("no sources merit newspaper coverage")
	}
	var out selectionOutput
	err := call("selection", selectionRules+"Select up to twelve stories for the whole edition. Aim for eight to twelve when worthwhile evidence supports them, but fewer is correct on quiet days. Put the lead and two strongest supporting reports first, followed by concise briefs.", previews(pool, 12), &out)
	if err == nil && len(out.Groups) == 0 {
		err = errors.New("selection contains no stories")
	}
	if err == nil {
		for i, group := range out.Groups {
			if len(group) == 1 && len(companions[group[0]]) > 1 {
				out.Groups[i] = companions[group[0]]
			}
		}
	}
	return out, err
}

func articleDepth(position int) string {
	if position == 0 {
		return " Treatment: lead report, 160–220 words total. Earn the lead with explanation and a supported consequence, not a longer headline."
	}
	if position < 3 {
		return " Treatment: supporting report, 110–160 words total."
	}
	return " Treatment: concise brief, 60–100 words total. One summary, one short paragraph with the useful detail or limitation, and a specific implication. Do not inflate thin evidence."
}

type articlePreview struct {
	ID          string `json:"id"`
	Section     string `json:"section"`
	Headline    string `json:"headline"`
	Summary     string `json:"summary"`
	Implication string `json:"implication"`
}
type rankingOutput struct {
	ArticleIDs []string `json:"article_ids"`
}

func rankingInput(articles []content.Article) []articlePreview {
	result := []articlePreview{}
	for _, a := range articles {
		result = append(result, articlePreview{a.ID, a.Section, excerpt(a.Headline, 140), excerpt(a.Summary, 240), excerpt(a.Implication, 180)})
	}
	return result
}

func validateSelection(p selectionPacket, out selectionOutput) error {
	if len(out.Groups) > p.Limit {
		return &StageValidationError{"selection exceeds the story limit"}
	}
	available, seen := map[string]bool{}, map[string]bool{}
	for _, s := range p.Stories {
		available[s.ID] = true
	}
	for _, group := range out.Groups {
		if len(group) == 0 || len(group) > 2 {
			return &StageValidationError{"each story needs one or two source IDs"}
		}
		for _, id := range group {
			if !available[id] || seen[id] {
				return &StageValidationError{"selection contains an unknown or repeated source ID"}
			}
			seen[id] = true
		}
	}
	return nil
}

// Retain opening context and verbatim paragraphs with mechanisms, limitations
// and fixes. Ellipses explicitly mark gaps; no generated summary becomes evidence.
func evidenceExcerpt(text, title string, limit int) string {
	if len([]rune(text)) <= limit {
		return text
	}
	parts := strings.Split(text, "\n\n")
	if len(parts) < 3 {
		return excerpt(text, limit)
	}
	selected := map[int]bool{0: true}
	used := len([]rune(excerpt(parts[0], min(800, limit))))
	terms := strings.Fields(strings.ToLower(title))
	for {
		best, score := -1, -1
		for i, part := range parts {
			if selected[i] || used+len([]rune(part))+32 > limit {
				continue
			}
			lower := strings.ToLower(part)
			s := 0
			for _, term := range terms {
				if len(term) > 3 && strings.Contains(lower, term) {
					s++
				}
			}
			for _, term := range []string{"fixed", "affected", "limit", "however", "requires", "because", "experimental", "preview", "benchmark", "instead", "only"} {
				if strings.Contains(lower, term) {
					s += 2
				}
			}
			if s > score {
				best, score = i, s
			}
		}
		if best < 0 {
			break
		}
		selected[best] = true
		used += len([]rune(parts[best])) + 32
	}
	result := []string{excerpt(parts[0], min(800, limit))}
	previous := 0
	for i := 1; i < len(parts); i++ {
		if selected[i] {
			if i > previous+1 || (previous == 0 && result[0] != parts[0]) {
				result = append(result, "[...]")
			}
			result = append(result, parts[i])
			previous = i
		}
	}
	return strings.Join(result, "\n\n")
}
