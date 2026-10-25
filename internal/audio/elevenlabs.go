package audio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"time"
)

// ElevenLabs remains an explicit opt-in provider; there is no paid fallback.
type ElevenLabs struct {
	APIKey, Voice, Model string
	Client               *http.Client
}

func (n ElevenLabs) Identity() (string, error) {
	if n.APIKey == "" || !regexp.MustCompile(`^[a-zA-Z0-9_-]+$`).MatchString(n.Voice) || n.Model == "" {
		return "", errors.New("configure ElevenLabs credentials, voice and model")
	}
	// Keep the original identity so existing recordings remain reusable.
	return n.Voice + "\x00" + n.Model + "\x00mp3_44100_128", nil
}
func (n ElevenLabs) Synthesize(ctx context.Context, script string) (speechResponse, error) {
	var speech speechResponse
	payload, _ := json.Marshal(map[string]string{"text": script, "model_id": n.Model})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.elevenlabs.io/v1/text-to-speech/"+n.Voice+"/with-timestamps?output_format=mp3_44100_128", bytes.NewReader(payload))
	if err != nil {
		return speechResponse{}, err
	}
	req.Header.Set("xi-api-key", n.APIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	client := n.Client
	if client == nil {
		client = &http.Client{Timeout: 3 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("speech redirects disabled") }}
	}
	resp, err := client.Do(req)
	if err != nil {
		return speechResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return speechResponse{}, speechError(resp)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (24<<20)+1))
	if err != nil {
		return speechResponse{}, err
	}
	if len(raw) > 24<<20 {
		return speechResponse{}, errors.New("speech response exceeds size limit")
	}
	if err := json.Unmarshal(raw, &speech); err != nil {
		return speechResponse{}, errors.New("invalid speech response JSON")
	}
	if len(speech.Audio) == 0 || len(speech.Audio) > 12<<20 {
		return speechResponse{}, errors.New("invalid audio response size")
	}
	return speech, nil
}
