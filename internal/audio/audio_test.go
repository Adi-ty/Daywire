package audio

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Adi-ty/daywire/internal/content"
	"github.com/Adi-ty/daywire/internal/store"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type uploader struct {
	fail  bool
	calls int
}

func (u *uploader) Put(_ context.Context, key, mime string, data []byte) (string, error) {
	u.calls++
	if u.fail {
		return "", errors.New("temporary upload failure")
	}
	return "https://media.example.com/" + key, nil
}

func setupAudio(t *testing.T, duration string) (Narrator, content.Edition, *uploader, *int) {
	t.Helper()
	dir := t.TempDir()
	probe := filepath.Join(dir, "ffprobe")
	if err := os.WriteFile(probe, []byte("#!/bin/sh\nprintf '%s\\n' '"+duration+"'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	var e content.Edition
	if err := store.Read("../../testdata/demo/2026-10-23.json", &e); err != nil {
		t.Fatal(err)
	}
	e.Demo = false
	count := new(int)
	upload := &uploader{}
	n := Narrator{Store: upload, Speaker: ElevenLabs{APIKey: "test-key", Voice: "test-voice", Model: "test-model", Client: &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		*count++
		if r.Header.Get("xi-api-key") != "test-key" {
			t.Fatal("missing provider authentication")
		}
		if !strings.Contains(r.URL.Path, "/with-timestamps") {
			t.Fatal("missing timing endpoint")
		}
		alignment := aligned(e.Content.Script(), .005)
		raw, _ := json.Marshal(speechResponse{Audio: []byte("fake audio bytes for the stubbed decoder"), Alignment: alignment})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(raw))), Header: http.Header{}}, nil
	})}}}
	return n, e, upload, count
}

func TestUploadRetryReusesCompletedSpeech(t *testing.T) {
	n, e, upload, calls := setupAudio(t, "42.5")
	state := t.TempDir()
	upload.fail = true
	if _, err := n.Produce(context.Background(), e, state); err == nil {
		t.Fatal("expected upload failure")
	}
	upload.fail = false
	ready, err := n.Produce(context.Background(), e, state)
	if err != nil {
		t.Fatal(err)
	}
	if *calls != 1 || upload.calls != 2 || len(ready.Radio.Cues) == 0 || ready.Radio.Status != "ready" || *ready.Radio.DurationSeconds != 42.5 {
		t.Fatalf("speech calls=%d uploads=%d radio=%+v", *calls, upload.calls, ready.Radio)
	}
}
func TestAudioDurationSafetyCeiling(t *testing.T) {
	for _, duration := range []string{"301", "420", "600", "600.01", "0", "-1", "NaN", "+Inf"} {
		t.Run(duration, func(t *testing.T) {
			n, e, upload, _ := setupAudio(t, duration)
			e.Radio.Status = "failed" // A retry can recover an edition without audio.
			ready, err := n.Produce(context.Background(), e, t.TempDir())
			valid := duration == "301" || duration == "420" || duration == "600"
			if valid {
				if err != nil {
					t.Fatal(err)
				}
				if upload.calls != 1 || ready.Radio.Status != "ready" {
					t.Fatalf("audio was not published: uploads=%d status=%s", upload.calls, ready.Radio.Status)
				}
			} else if err == nil || upload.calls != 0 {
				t.Fatalf("invalid duration accepted or uploaded: err=%v uploads=%d", err, upload.calls)
			}
		})
	}
}
func TestDemoNeverCallsSpeechProvider(t *testing.T) {
	n, e, _, calls := setupAudio(t, "20")
	e.Demo = true
	if _, err := n.Produce(context.Background(), e, t.TempDir()); err == nil {
		t.Fatal("accepted demo")
	}
	if *calls != 0 {
		t.Fatal("made paid call for demo")
	}
}
