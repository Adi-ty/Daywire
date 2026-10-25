package editorial

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Adi-ty/daywire/internal/content"
	"github.com/Adi-ty/daywire/internal/store"
)

type lessonContractModel struct {
	lesson    content.Lesson
	calls     int
	permanent bool
}

func (m *lessonContractModel) Generate(_ context.Context, _ string, _ any, out any) (Usage, error) {
	m.calls++
	v := out.(*lessonOutput)
	v.Lesson = m.lesson
	if m.permanent || m.calls == 1 {
		v.Lesson.Example = ""
	}
	return Usage{TotalTokens: 10}, nil
}
func TestLessonCorrectionBeforeCaching(t *testing.T) {
	e, _, _ := repairFixture(t)
	for _, permanent := range []bool{false, true} {
		m := &lessonContractModel{lesson: e.Content.Lessons[0], permanent: permanent}
		cache := &CachedModel{Model: m, Directory: t.TempDir(), Identity: "test"}
		var out lessonOutput
		usage, err := generateValidated(context.Background(), cache, "write", struct{}{}, &out)
		if m.calls != 2 || usage.TotalTokens != 20 {
			t.Fatalf("calls=%d usage=%+v", m.calls, usage)
		}
		entries, _ := os.ReadDir(cache.Directory)
		if permanent {
			var invalid *StageValidationError
			if !errors.As(err, &invalid) || len(entries) != 0 {
				t.Fatalf("invalid lesson cached/accepted: %v", err)
			}
		} else if err != nil || out.Lesson.Example == "" || len(entries) != 1 {
			t.Fatalf("correction failed: %v", err)
		}
	}
}
func TestLegacyInvalidLessonCacheIsRegenerated(t *testing.T) {
	e, _, _ := repairFixture(t)
	m := &lessonContractModel{lesson: e.Content.Lessons[0], calls: 1}
	cache := &CachedModel{Model: m, Directory: t.TempDir(), Identity: "test"}
	var out lessonOutput
	if _, err := cache.Generate(context.Background(), "write", struct{}{}, &out); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(cache.Directory)
	path := filepath.Join(cache.Directory, entries[0].Name())
	var saved stageCheckpoint
	if err := store.Read(path, &saved); err != nil {
		t.Fatal(err)
	}
	out.Lesson.Example = ""
	raw, _ := json.Marshal(out)
	saved.Output = string(raw)
	if err := store.JSON(path, saved); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Generate(context.Background(), "write", struct{}{}, &out); err != nil {
		t.Fatal(err)
	}
	if m.calls != 3 || out.Lesson.Example == "" {
		t.Fatal("invalid legacy lesson reused")
	}
}

type invalidLessonRepair struct{ calls int }

func (m *invalidLessonRepair) Generate(_ context.Context, _ string, in, out any) (Usage, error) {
	m.calls++
	v, ok := out.(*lessonOutput)
	if !ok {
		return Usage{}, errors.New("dependent stage ran before lesson was valid")
	}
	v.Lesson = in.(repairInput).Packet.Draft.Lessons[0]
	v.Lesson.Example = ""
	return Usage{TotalTokens: 1}, nil
}
func TestRepairStopsBeforeDependentsWhenLessonInvalid(t *testing.T) {
	e, input, review := repairFixture(t)
	m := &invalidLessonRepair{}
	_, _, err := Repair(context.Background(), m, input, e.Content, review, nil)
	var invalid *StageValidationError
	if !errors.As(err, &invalid) || m.calls != 2 {
		t.Fatalf("calls=%d err=%v", m.calls, err)
	}
}
