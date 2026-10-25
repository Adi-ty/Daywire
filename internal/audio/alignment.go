package audio

import (
	"errors"
	"math"
	"strings"
	"unicode"

	"github.com/Adi-ty/daywire/internal/content"
)

type Alignment struct {
	Characters []string  `json:"characters"`
	Starts     []float64 `json:"character_start_times_seconds"`
	Ends       []float64 `json:"character_end_times_seconds"`
}
type speechResponse struct {
	Audio     []byte     `json:"audio_base64"`
	Alignment *Alignment `json:"alignment"`
}

func BuildCues(segments []content.Segment, a *Alignment) ([]content.TranscriptCue, error) {
	if a == nil || len(a.Characters) == 0 || len(a.Starts) != len(a.Characters) || len(a.Ends) != len(a.Characters) {
		return nil, errors.New("missing or incomplete speech alignment")
	}
	type word struct {
		text       string
		start, end float64
	}
	words := []word{}
	var text strings.Builder
	first, last := -1, -1
	flush := func() {
		if first >= 0 {
			words = append(words, word{text.String(), a.Starts[first], a.Ends[last]})
			text.Reset()
			first, last = -1, -1
		}
	}
	previous := -1.0
	for i, character := range a.Characters {
		if math.IsNaN(a.Starts[i]) || math.IsNaN(a.Ends[i]) || math.IsInf(a.Starts[i], 0) || math.IsInf(a.Ends[i], 0) || a.Starts[i] < 0 || a.Starts[i] < previous || a.Ends[i] < a.Starts[i] {
			return nil, errors.New("invalid speech alignment times")
		}
		previous = a.Starts[i]
		for _, r := range character {
			if unicode.IsSpace(r) {
				flush()
			} else {
				if first < 0 {
					first = i
				}
				last = i
				text.WriteRune(r)
			}
		}
	}
	flush()
	cues := []content.TranscriptCue{}
	index := 0
	for _, segment := range segments {
		for _, expected := range strings.Fields(segment.Text) {
			if index >= len(words) || words[index].text != expected {
				return nil, errors.New("speech alignment does not match the accepted script")
			}
			w := words[index]
			cues = append(cues, content.TranscriptCue{SegmentID: segment.ID, Text: w.text, StartSeconds: w.start, EndSeconds: w.end})
			index++
		}
	}
	if index != len(words) {
		return nil, errors.New("speech alignment has extra words")
	}
	return cues, nil
}
