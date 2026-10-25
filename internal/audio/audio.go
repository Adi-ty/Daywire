package audio

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Adi-ty/daywire/internal/content"
	"github.com/Adi-ty/daywire/internal/store"
)

type Uploader interface {
	Put(context.Context, string, string, []byte) (string, error)
}

// Speaker supplies encoded audio and optional model-derived timing.
// Identity must cover every setting that changes the generated waveform.
type Speaker interface {
	Identity() (string, error)
	Synthesize(context.Context, string) (speechResponse, error)
}
type Narrator struct {
	Speaker Speaker
	Store   Uploader
}
type Recording struct {
	Key      string
	Data     []byte
	Duration float64
	Cues     []content.TranscriptCue
}

// Render is also used for a local MP3 preview: it never uploads or edits an edition.
func (n Narrator) Render(ctx context.Context, e content.Edition, stateDir string) (Recording, error) {
	if err := content.Validate(e); err != nil {
		return Recording{}, err
	}
	if e.Demo {
		return Recording{}, errors.New("demo editions cannot generate narration")
	}
	if n.Speaker == nil {
		return Recording{}, errors.New("configure a speech provider")
	}
	identity, err := n.Speaker.Identity()
	if err != nil {
		return Recording{}, err
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		return Recording{}, errors.New("ffprobe is required to enforce the five-minute audio limit")
	}

	script := e.Content.Script()
	key := content.Hash([]byte(script + "\x00" + identity))
	path := filepath.Join(stateDir, "audio", key+".mp3")
	timingPath := filepath.Join(stateDir, "audio", key+".speech.json")
	var speech speechResponse
	if raw, readErr := os.ReadFile(timingPath); readErr == nil {
		if err := json.Unmarshal(raw, &speech); err != nil {
			return Recording{}, errors.New("invalid cached speech response")
		}
	} else if !os.IsNotExist(readErr) {
		return Recording{}, readErr
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return Recording{}, err
		}
		if len(speech.Audio) == 0 {
			speech, err = n.Speaker.Synthesize(ctx, script)
			if err != nil {
				return Recording{}, err
			}
			if len(speech.Audio) == 0 || len(speech.Audio) > 12<<20 {
				return Recording{}, errors.New("invalid audio response size")
			}
			// Save audio and alignment together before upload, so retries do not
			// repeat speech generation if writing the extracted MP3 is interrupted.
			if err := store.JSON(timingPath, speech); err != nil {
				return Recording{}, err
			}
		}
		data = speech.Audio
		if err := store.Write(path, data, 0600); err != nil {
			return Recording{}, err
		}
	}
	probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	raw, err := exec.CommandContext(probeCtx, "ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", path).Output()
	if err != nil {
		return Recording{}, errors.New("generated audio could not be decoded by ffprobe")
	}
	duration, err := strconv.ParseFloat(strings.TrimSpace(string(raw)), 64)
	if err != nil || math.IsNaN(duration) || math.IsInf(duration, 0) || duration <= 0 || duration > 300 {
		return Recording{}, errors.New("audio duration must be greater than zero and at most 300 seconds; revise the script before regenerating")
	}
	recording := Recording{Key: key, Data: data, Duration: duration}
	// Never fabricate timestamps if the provider's alignment is absent or
	// normalized differently. The transcript remains readable without following.
	if cues, cueErr := BuildCues(e.Content.Segments, speech.Alignment); cueErr == nil {
		withinDuration := true
		for _, cue := range cues {
			if cue.EndSeconds > duration+0.25 {
				withinDuration = false
				break
			}
		}
		if withinDuration {
			recording.Cues = cues
		}
	}
	return recording, nil
}

func (n Narrator) Produce(ctx context.Context, e content.Edition, stateDir string) (content.Edition, error) {
	if err := content.Validate(e); err != nil {
		return e, err
	}
	if e.Demo {
		return e, errors.New("demo editions cannot generate narration")
	}
	if e.Radio.Status == "ready" {
		return e, nil
	}
	if n.Store == nil {
		return e, errors.New("configure media storage")
	}
	recording, err := n.Render(ctx, e, stateDir)
	if err != nil {
		return e, err
	}
	publicURL, err := n.Store.Put(ctx, "radio/"+e.Date+"/"+recording.Key+".mp3", "audio/mpeg", recording.Data)
	if err != nil {
		return e, err
	}
	e.Radio.Status = "ready"
	e.Radio.AudioURL = &publicURL
	e.Radio.DurationSeconds = &recording.Duration
	e.Radio.Cues = recording.Cues
	return e, content.Validate(e)
}
