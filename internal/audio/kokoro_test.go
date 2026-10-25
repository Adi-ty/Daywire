package audio

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func helperStub(t *testing.T, python, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "python-stub")
	if err := os.WriteFile(path, []byte("#!"+python+"\n"+body), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestKokoroCachesSpeechAcrossUploadFailure(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python needed for the subprocess contract test")
	}
	n, e, upload, _ := setupAudio(t, "10")
	// Only ffmpeg discovery is stubbed here; inference protocol is exercised by
	// a real child process, and no model/dependency downloads are needed.
	if err := os.WriteFile(filepath.Join(os.Getenv("PATH"), "ffmpeg"), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GROQ_API_KEY", "must-not-reach-helper")
	t.Setenv("R2_SECRET_ACCESS_KEY", "must-not-reach-helper")
	var logs bytes.Buffer
	stub := helperStub(t, python, `import sys, json, os, base64
assert "GROQ_API_KEY" not in os.environ and "R2_SECRET_ACCESS_KEY" not in os.environ
assert "HF_HOME" in os.environ or "HF_HUB_CACHE" in os.environ
request = json.load(open(sys.argv[3]))
assert request["voice"] == "af_heart" and request["speed"] == 1
assert len(request["text"]) > 0
json.dump({"audio_base64": base64.b64encode(b"stub mp3").decode(), "alignment": None}, open(sys.argv[5], "w"))
print("inference completed")
`)
	n.Speaker = Kokoro{Python: stub, Voice: "af_heart", Speed: 1, Log: &logs}
	state := t.TempDir()
	upload.fail = true
	if _, err := n.Produce(context.Background(), e, state); err == nil {
		t.Fatal("expected upload failure")
	}
	// Delete Python to prove the upload retry needs no running speech engine.
	if err := os.Remove(stub); err != nil {
		t.Fatal(err)
	}
	upload.fail = false
	ready, err := n.Produce(context.Background(), e, state)
	if err != nil {
		t.Fatal(err)
	}
	if ready.Radio.Status != "ready" || len(ready.Radio.Cues) != 0 || strings.Count(logs.String(), "inference completed") != 1 || upload.calls != 2 {
		t.Fatalf("bad retry: status=%s uploads=%d logs=%s", ready.Radio.Status, upload.calls, logs.String())
	}
}

func TestKokoroCancellationStopsHelper(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python unavailable")
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "ffmpeg"), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	k := Kokoro{Python: helperStub(t, python, "import time\ntime.sleep(60)\n"), Voice: "af_heart", Speed: 1}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = k.Synthesize(ctx, "A short bulletin.")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline: %v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("cancellation did not stop the helper promptly")
	}
}

func TestKokoroCacheIdentityAndConfiguration(t *testing.T) {
	k := Kokoro{Voice: "af_heart", Speed: 1}
	one, err := k.Identity()
	if err != nil {
		t.Fatal(err)
	}
	k.Speed = 1.1
	two, _ := k.Identity()
	k.Voice = "bm_george"
	three, _ := k.Identity()
	if one == two || two == three {
		t.Fatal("voice and speed must invalidate the audio cache")
	}
	k.Voice = "../../voice"
	if _, err := k.Identity(); err == nil {
		t.Fatal("accepted voice path")
	}
	k.Voice, k.Speed = "af_heart", 0
	if _, err := k.Identity(); err == nil {
		t.Fatal("accepted invalid speed")
	}
}

func TestLocalRenderDoesNotUpload(t *testing.T) {
	n, e, upload, calls := setupAudio(t, "20")
	before := e.Radio.Status
	recording, err := n.Render(context.Background(), e, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if recording.Duration != 20 || upload.calls != 0 || *calls != 1 || e.Radio.Status != before {
		t.Fatal("local rendering changed publication state")
	}
}
