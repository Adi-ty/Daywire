package audio

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Adi-ty/daywire/internal/content"
	"github.com/Adi-ty/daywire/internal/store"
)

// Embed the helper so the compiled CLI does not depend on its working directory.
//
//go:embed kokoro/generate.py kokoro/model.json kokoro/requirements.txt
var kokoroFiles embed.FS

type Kokoro struct {
	Python, Voice string
	Device        string
	Speed         float64
	Log           io.Writer
}

func (k Kokoro) device() string {
	if k.Device == "" {
		return "cpu"
	}
	return k.Device
}

func (k Kokoro) Identity() (string, error) {
	if k.device() != "cpu" && k.device() != "mps" {
		return "", errors.New("KOKORO_DEVICE must be cpu or mps (Apple Silicon)")
	}
	if !regexp.MustCompile(`^[ab][fm]_[a-z]+$`).MatchString(k.Voice) {
		return "", errors.New("KOKORO_VOICE must be an English Kokoro voice such as af_heart or bm_george")
	}
	if math.IsNaN(k.Speed) || math.IsInf(k.Speed, 0) || k.Speed < 0.75 || k.Speed > 1.5 {
		return "", errors.New("KOKORO_SPEED must be between 0.75 and 1.5")
	}
	var recipe []byte
	for _, name := range []string{"generate.py", "model.json", "requirements.txt"} {
		data, err := kokoroFiles.ReadFile("kokoro/" + name)
		if err != nil {
			return "", err
		}
		recipe = append(recipe, data...)
	}
	return "kokoro\x00" + content.Hash(recipe) + "\x00" + k.Voice + "\x00" + strconv.FormatFloat(k.Speed, 'f', -1, 64) + "\x00" + k.device(), nil
}

func (k Kokoro) Synthesize(ctx context.Context, script string) (speechResponse, error) {
	if _, err := k.Identity(); err != nil {
		return speechResponse{}, err
	}
	if _, err := exec.LookPath(k.Python); err != nil {
		return speechResponse{}, errors.New("Kokoro Python environment missing; run bash scripts/setup-kokoro.sh (or set KOKORO_PYTHON)")
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return speechResponse{}, errors.New("Kokoro requires ffmpeg for MP3 encoding")
	}
	dir, err := os.MkdirTemp("", "daywire-kokoro-")
	if err != nil {
		return speechResponse{}, err
	}
	defer os.RemoveAll(dir)
	for _, name := range []string{"generate.py", "model.json"} {
		data, err := kokoroFiles.ReadFile("kokoro/" + name)
		if err != nil {
			return speechResponse{}, err
		}
		if err := store.Write(filepath.Join(dir, name), data, 0600); err != nil {
			return speechResponse{}, err
		}
	}
	request := filepath.Join(dir, "request.json")
	response := filepath.Join(dir, "response.json")
	if err := store.JSON(request, map[string]any{"text": script, "voice": k.Voice, "speed": k.Speed, "device": k.device()}); err != nil {
		return speechResponse{}, err
	}
	childCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(childCtx, k.Python, filepath.Join(dir, "generate.py"), "--request", request, "--response", response)
	// Cancel the helper and its encoder together on macOS/Linux.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
			if errors.Is(err, syscall.ESRCH) {
				return os.ErrProcessDone
			}
			return err
		}
		return nil
	}
	// Speech inference needs no application credentials. Keep only runtime/cache
	// settings, so unrelated Groq/R2/ElevenLabs secrets never reach Python.
	for _, pair := range os.Environ() {
		name, _, _ := strings.Cut(pair, "=")
		switch name {
		case "PATH", "HOME", "TMPDIR", "SYSTEMROOT", "LANG", "LC_ALL", "HF_HOME", "HF_HUB_CACHE", "HF_HUB_OFFLINE", "SSL_CERT_FILE", "SSL_CERT_DIR", "REQUESTS_CA_BUNDLE", "PHONEMIZER_ESPEAK_LIBRARY":
			cmd.Env = append(cmd.Env, pair)
		}
	}
	cmd.Env = append(cmd.Env, "PYTHONUNBUFFERED=1", "HF_HUB_DISABLE_TELEMETRY=1", "TOKENIZERS_PARALLELISM=false")
	if os.Getenv("HF_HOME") == "" && os.Getenv("HF_HUB_CACHE") == "" {
		cache, err := filepath.Abs(".cache/kokoro/models")
		if err != nil {
			return speechResponse{}, err
		}
		cmd.Env = append(cmd.Env, "HF_HOME="+cache)
	}
	cmd.Stdout, cmd.Stderr = k.Log, k.Log
	cmd.WaitDelay = 3 * time.Second
	if err := cmd.Run(); err != nil {
		if childCtx.Err() != nil {
			return speechResponse{}, fmt.Errorf("Kokoro stopped: %w", childCtx.Err())
		}
		return speechResponse{}, fmt.Errorf("Kokoro failed (%w); see the speech helper output above", err)
	}
	f, err := os.Open(response)
	if err != nil {
		return speechResponse{}, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, (24<<20)+1))
	if err != nil {
		return speechResponse{}, err
	}
	if len(raw) > 24<<20 {
		return speechResponse{}, errors.New("Kokoro response exceeds size limit")
	}
	var speech speechResponse
	if err := json.Unmarshal(raw, &speech); err != nil {
		return speechResponse{}, errors.New("invalid Kokoro response")
	}
	return speech, nil
}
