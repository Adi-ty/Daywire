package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
	_ "time/tzdata"

	"github.com/Adi-ty/daywire/internal/audio"
	"github.com/Adi-ty/daywire/internal/content"
	"github.com/Adi-ty/daywire/internal/editorial"
	"github.com/Adi-ty/daywire/internal/evaluation"
	"github.com/Adi-ty/daywire/internal/pipeline"
	"github.com/Adi-ty/daywire/internal/store"
)

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
func mediaStore() store.R2 {
	return store.R2{AccountID: os.Getenv("R2_ACCOUNT_ID"), Bucket: os.Getenv("R2_BUCKET"), AccessKey: os.Getenv("R2_ACCESS_KEY_ID"), SecretKey: os.Getenv("R2_SECRET_ACCESS_KEY"), PublicURL: os.Getenv("MEDIA_PUBLIC_URL")}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" {
		fmt.Println("Daywire — daily technology newspaper and radio\n\nCommands: demo, generate, validate, schema, radio, artwork, checkpoint, eval\nRun a command with -h for its options. Live commands read exported environment variables; .env is never loaded automatically.")
		return nil
	}
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	state := fs.String("state", ".daywire", "private checkpoint directory")
	output := fs.String("out", "content/editions", "public edition directory (schema/eval: output file)")
	file := fs.String("file", "", "edition JSON path, or image path for artwork")
	zone, _ := time.LoadLocation("Asia/Kolkata")
	date := fs.String("date", time.Now().In(zone).Format("2006-01-02"), "edition date in Asia/Kolkata")
	sources := fs.String("sources", "config/sources.json", "source registry")
	dataset := fs.String("dataset", "evals/development.json", "frozen AI evaluation cases")
	modelName := fs.String("model", env("GROQ_MODEL", "openai/gpt-oss-120b"), "Groq structured-output model")
	editionPath := fs.String("edition", "", "edition JSON to attach artwork to")
	alt := fs.String("alt", "", "description of reviewed artwork")
	evalTimeout := fs.Duration("eval-timeout", 20*time.Minute, "maximum duration of the full AI eval run")
	caseTimeout := fs.Duration("case-timeout", 5*time.Minute, "maximum duration per AI eval case, including rate-limit waits")
	direction := fs.String("direction", "save", "checkpoint direction: save or restore")
	audioOut := fs.String("audio-out", "", "radio: save a local MP3 only, without R2 or changing the edition")
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	switch args[0] {
	case "checkpoint":
		if _, _, err := pipeline.Window(*date); err != nil {
			return err
		}
		r := mediaStore()
		r.Bucket = os.Getenv("R2_STATE_BUCKET")
		r.PublicURL = ""
		if r.Bucket == "" || r.Bucket == os.Getenv("R2_BUCKET") {
			return errors.New("R2_STATE_BUCKET must be a separate private bucket")
		}
		key := "runs/" + *date + ".tar.gz"
		if *direction == "save" {
			data, err := store.Pack(*state)
			if err != nil {
				return err
			}
			_, err = r.Put(ctx, key, "application/gzip", data)
			return err
		}
		if *direction != "restore" {
			return errors.New("direction must be save or restore")
		}
		data, err := r.Get(ctx, key, 64<<20)
		if os.IsNotExist(err) {
			fmt.Println("No previous checkpoint for this date")
			return nil
		}
		if err != nil {
			return err
		}
		return store.Unpack(*state, data)
	case "schema":
		path := *output
		if path == "content/editions" {
			path = "schemas/edition.schema.json"
		}
		return store.JSON(path, content.Schema(content.Edition{}))
	case "demo":
		var e content.Edition
		if err := store.Read("testdata/demo/2026-10-23.json", &e); err != nil {
			return err
		}
		if err := content.Validate(e); err != nil {
			return err
		}
		if !e.Demo {
			return errors.New("demo fixture must be labeled")
		}
		path := filepath.Join("content/demo", e.Date+".json")
		if err := store.JSON(path, e); err != nil {
			return err
		}
		fmt.Println("Demo edition ready:", path, "(fictional content, no API calls)")
		return nil
	case "validate":
		if *file != "" {
			var e content.Edition
			if err := store.Read(*file, &e); err != nil {
				return err
			}
			return content.Validate(e)
		}
		entries, err := os.ReadDir(*output)
		if err != nil {
			return err
		}
		count := 0
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
				continue
			}
			var e content.Edition
			if err := store.Read(filepath.Join(*output, entry.Name()), &e); err != nil {
				return err
			}
			if e.Date+".json" != entry.Name() {
				return errors.New("edition filename does not match its date")
			}
			if err := content.Validate(e); err != nil {
				return fmt.Errorf("%s: %w", entry.Name(), err)
			}
			count++
		}
		if count == 0 {
			return errors.New("no edition files found")
		}
		fmt.Printf("Validated %d editions\n", count)
		return nil
	case "generate", "radio", "artwork":
		if err := os.MkdirAll(*state, 0700); err != nil {
			return err
		}
		lockPath := filepath.Join(*state, "run.lock")
		lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return fmt.Errorf("cannot acquire run lock (if a previous process crashed, remove %s after checking it is stopped): %w", lockPath, err)
		}
		lock.Close()
		defer os.Remove(lockPath)
		if args[0] == "generate" {
			if _, _, err := pipeline.Window(*date); err != nil {
				return err
			}
			if os.Getenv("GROQ_API_KEY") == "" {
				if _, err := os.Stat(filepath.Join(*output, *date+".json")); err != nil {
					return errors.New("export GROQ_API_KEY before generating a live edition")
				}
			}
			model := editorial.NewGroq(os.Getenv("GROQ_API_KEY"), *modelName)
			model.DiagnosticsDir = filepath.Join(*state, *date, "diagnostics")
			model.Logf = func(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...) }
			e, err := pipeline.Run(ctx, pipeline.Options{Date: *date, SourcesPath: *sources, StateDir: *state, OutputDir: *output, ModelName: *modelName, Model: model, Logf: model.Logf})
			if err != nil {
				return err
			}
			fmt.Printf("Edition %s ready in %s; radio %s\n", e.Date, *output, e.Radio.Status)
			return nil
		}
		path := *file
		if args[0] == "artwork" {
			path = *editionPath
		}
		if path == "" {
			return errors.New("edition file is required")
		}
		var e content.Edition
		if err := store.Read(path, &e); err != nil {
			return err
		}
		if args[0] == "radio" {
			var speaker audio.Speaker
			switch env("TTS_PROVIDER", "kokoro") {
			case "kokoro":
				speed, err := strconv.ParseFloat(env("KOKORO_SPEED", "1.0"), 64)
				if err != nil {
					return errors.New("KOKORO_SPEED must be a number between 0.75 and 1.5")
				}
				speaker = audio.Kokoro{Python: env("KOKORO_PYTHON", ".venv-kokoro/bin/python"), Voice: env("KOKORO_VOICE", "af_heart"), Device: env("KOKORO_DEVICE", "cpu"), Speed: speed, Log: os.Stderr}
			case "elevenlabs":
				speaker = audio.ElevenLabs{APIKey: os.Getenv("ELEVENLABS_API_KEY"), Voice: os.Getenv("ELEVENLABS_VOICE_ID"), Model: env("ELEVENLABS_MODEL", "eleven_multilingual_v2")}
			default:
				return errors.New("TTS_PROVIDER must be kokoro or elevenlabs")
			}
			n := audio.Narrator{Speaker: speaker, Store: mediaStore()}
			if *audioOut != "" {
				if filepath.Ext(*audioOut) != ".mp3" {
					return errors.New("--audio-out must be an .mp3 file")
				}
				recording, err := n.Render(ctx, e, *state)
				if err != nil {
					return err
				}
				if err := store.Write(*audioOut, recording.Data, 0600); err != nil {
					return err
				}
				fmt.Printf("Local narration ready: %s (%.1fs, %d word cues); edition unchanged\n", *audioOut, recording.Duration, len(recording.Cues))
				return nil
			}
			if !content.WebURL(os.Getenv("MEDIA_PUBLIC_URL")) {
				return errors.New("MEDIA_PUBLIC_URL must be a public HTTPS base URL")
			}
			if err := mediaStore().Validate(); err != nil {
				return err
			}
			updated, err := n.Produce(ctx, e, *state)
			if err != nil {
				if !e.Demo && content.Validate(e) == nil && e.Radio.Status != "ready" {
					e.Radio.Status = "failed"
					if saveErr := store.JSON(path, e); saveErr != nil {
						return errors.Join(err, saveErr)
					}
				}
				return err
			}
			if err := store.JSON(path, updated); err != nil {
				return err
			}
			fmt.Printf("Radio ready for %s: %s\n", updated.Date, *updated.Radio.AudioURL)
			return nil
		}
		if err := content.Validate(e); err != nil {
			return err
		}
		if e.Demo {
			return errors.New("demo artwork cannot be uploaded")
		}
		if !content.WebURL(os.Getenv("MEDIA_PUBLIC_URL")) {
			return errors.New("MEDIA_PUBLIC_URL must be a public HTTPS base URL")
		}
		info, err := os.Stat(*file)
		if err != nil {
			return err
		}
		if info.Size() > 8<<20 {
			return errors.New("artwork exceeds 8 MB")
		}
		data, err := os.ReadFile(*file)
		if err != nil {
			return err
		}
		cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || cfg.Width < 640 || cfg.Height < 360 || cfg.Width > 8192 || cfg.Height > 8192 || *alt == "" {
			return errors.New("supply a PNG/JPEG between 640×360 and 8192×8192, with --alt")
		}
		url, err := mediaStore().Put(ctx, "artwork/"+e.Date+"/"+content.Hash(data)+"."+format, "image/"+format, data)
		if err != nil {
			return err
		}
		e.Artwork = content.Artwork{Status: "ready", URL: &url, Alt: *alt}
		if err := content.Validate(e); err != nil {
			return err
		}
		return store.JSON(path, e)
	case "eval":
		if os.Getenv("GROQ_API_KEY") == "" {
			return errors.New("live AI evals require GROQ_API_KEY")
		}
		var cases []evaluation.Case
		if err := store.Read(*dataset, &cases); err != nil {
			return err
		}
		if len(cases) == 0 || len(cases) > 100 {
			return errors.New("eval dataset must contain 1–100 cases")
		}
		if *evalTimeout <= 0 || *caseTimeout <= 0 {
			return errors.New("eval timeouts must be positive")
		}
		evalCtx, cancelEval := context.WithTimeout(ctx, *evalTimeout)
		defer cancelEval()
		started := time.Now()
		model := editorial.NewGroq(os.Getenv("GROQ_API_KEY"), *modelName)
		model.Logf = func(format string, args ...any) { fmt.Fprintf(os.Stderr, "  "+format+"\n", args...) }
		fmt.Fprintf(os.Stderr, "Running %d AI eval cases with %s (run limit %s; case limit %s)\n", len(cases), *modelName, *evalTimeout, *caseTimeout)
		progressDone := make(chan struct{})
		progressStopped := make(chan struct{})
		go func() {
			defer close(progressStopped)
			ticker := time.NewTicker(15 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-progressDone:
					return
				case <-ticker.C:
					fmt.Fprintf(os.Stderr, "  Eval still running (%s elapsed); Ctrl+C cancels and saves completed results.\n", time.Since(started).Round(time.Second))
				}
			}
		}()
		report := evaluation.RunWithOptions(evalCtx, model, *modelName, cases, evaluation.Options{CaseTimeout: *caseTimeout, Progress: func(index, total int, id string, result *evaluation.Result) {
			if result == nil {
				fmt.Fprintf(os.Stderr, "[%d/%d] %s: started\n", index, total, id)
				return
			}
			status := "matched"
			if !result.Match {
				status = "failed"
			}
			fmt.Fprintf(os.Stderr, "[%d/%d] %s: %s (%s elapsed)\n", index, total, id, status, time.Since(started).Round(time.Second))
		}})
		close(progressDone)
		<-progressStopped
		path := *output
		if path == "content/editions" {
			path = ".daywire/eval-report.json"
		}
		if err := store.JSON(path, report); err != nil {
			return err
		}
		matched := 0
		for _, result := range report.Cases {
			if result.Match {
				matched++
			}
		}
		summary, _ := json.Marshal(map[string]any{"cases": len(report.Cases), "total": len(cases), "skipped": len(cases) - len(report.Cases), "matched": matched, "errors": report.Errors, "false_accepts": report.FalseAccepts, "false_rejects": report.FalseRejects, "report": path})
		fmt.Println(string(summary))
		for _, result := range report.Cases {
			if !result.Match {
				fmt.Fprintf(os.Stderr, "%s: %s\n", result.ID, result.Error)
			}
		}
		if evalCtx.Err() != nil {
			return fmt.Errorf("AI eval stopped: %w; saved %d/%d results to %s", evalCtx.Err(), len(report.Cases), len(cases), path)
		}
		if report.Errors > 0 {
			return fmt.Errorf("AI eval incomplete: %d provider or verifier errors; see %s", report.Errors, path)
		}
		if len(report.Cases) != len(cases) || report.FalseAccepts > 0 || report.FalseRejects > 0 || report.Errors > 0 {
			return errors.New("AI eval expectations did not pass")
		}
		return nil
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func main() {
	root, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	ctx, cancel := context.WithTimeout(root, 30*time.Minute)
	err := run(ctx, os.Args[1:])
	cancel()
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "daywire:", err)
		os.Exit(1)
	}
}
