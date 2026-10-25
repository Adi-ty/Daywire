package audio

import (
	"context"
	"github.com/Adi-ty/daywire/internal/content"
	"math"
	"testing"
)

func aligned(text string, step float64) *Alignment {
	a := &Alignment{}
	for i, r := range []rune(text) {
		a.Characters = append(a.Characters, string(r))
		a.Starts = append(a.Starts, float64(i)*step)
		a.Ends = append(a.Ends, float64(i+1)*step)
	}
	return a
}
func TestBuildCuesPreservesScriptAndSegments(t *testing.T) {
	segments := []content.Segment{{ID: "a", Text: "Hello café."}, {ID: "b", Text: "你好 world!"}}
	cues, err := BuildCues(segments, aligned("Hello café.\n\n你好 world!", .1))
	if err != nil {
		t.Fatal(err)
	}
	if len(cues) != 4 || cues[1].Text != "café." || cues[2].SegmentID != "b" || cues[2].Text != "你好" || cues[0].StartSeconds != 0 || cues[0].EndSeconds != .5 {
		t.Fatalf("wrong cues: %+v", cues)
	}
}
func TestBuildCuesRejectsUnreliableAlignment(t *testing.T) {
	segments := []content.Segment{{ID: "a", Text: "Hello world"}}
	for _, name := range []string{"missing", "length", "mismatch", "extra", "backward", "nan", "end"} {
		t.Run(name, func(t *testing.T) {
			a := aligned("Hello world", .1)
			switch name {
			case "missing":
				a = nil
			case "length":
				a.Ends = nil
			case "mismatch":
				a.Characters[0] = "B"
			case "extra":
				a = aligned("Hello world extra", .1)
			case "backward":
				a.Starts[2] = 0
			case "nan":
				a.Ends[0] = math.NaN()
			case "end":
				a.Ends[2] = 0
			}
			if _, err := BuildCues(segments, a); err == nil {
				t.Fatal("accepted unreliable alignment")
			}
		})
	}
}
func TestAlignmentOutsideAudioFallsBackToReadableTranscript(t *testing.T) {
	n, e, _, _ := setupAudio(t, "0.5")
	ready, err := n.Produce(context.Background(), e, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(ready.Radio.Cues) != 0 || ready.Radio.Status != "ready" {
		t.Fatal("expected untimed ready audio")
	}
}
