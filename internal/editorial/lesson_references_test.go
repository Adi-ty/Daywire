package editorial

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Adi-ty/daywire/internal/content"
	"github.com/Adi-ty/daywire/internal/store"
)

func TestLessonReferencesMatchWritingPacket(t *testing.T) {
	e, input, _ := repairFixture(t)
	lesson := e.Content.Lessons[0]
	packet := lessonWritingInput{Packet: verificationSubset(input, e.Content, lesson.ArticleIDs)}
	for _, tc := range []struct {
		name     string
		articles []string
		sources  []string
		field    string
	}{
		{"valid", lesson.ArticleIDs, lesson.SourceIDs, ""},
		{"source IDs as articles", lesson.SourceIDs, lesson.SourceIDs, "article_ids"},
		{"missing articles", nil, lesson.SourceIDs, "article_ids"},
		{"duplicate articles", []string{lesson.ArticleIDs[0], lesson.ArticleIDs[0]}, lesson.SourceIDs, "article_ids"},
		{"article outside packet", []string{e.Content.Articles[1].ID}, lesson.SourceIDs, "article_ids"},
		{"article IDs as sources", lesson.ArticleIDs, lesson.ArticleIDs, "source_ids"},
		{"missing sources", lesson.ArticleIDs, nil, "source_ids"},
		{"duplicate sources", lesson.ArticleIDs, []string{lesson.SourceIDs[0], lesson.SourceIDs[0]}, "source_ids"},
		{"source outside packet", lesson.ArticleIDs, e.Content.Articles[1].SourceIDs, "source_ids"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := lessonOutput{Lesson: lesson}
			out.Lesson.ArticleIDs, out.Lesson.SourceIDs = tc.articles, tc.sources
			err := validStage(packet, &out)
			if tc.field == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var invalid *StageValidationError
			if !errors.As(err, &invalid) || !strings.Contains(invalid.Reason, tc.field) {
				t.Fatalf("expected retryable %s error, got %v", tc.field, err)
			}
		})
	}
}

type lessonReferenceWriter struct {
	stagedWriter
	lessonCalls int
	permanent   bool
}

func (m *lessonReferenceWriter) Generate(ctx context.Context, prompt string, in, out any) (Usage, error) {
	if lesson, ok := out.(*lessonOutput); ok {
		m.lessonCalls++
		if m.permanent || m.lessonCalls == 1 {
			lesson.Lesson = m.draft.Lessons[0]
			lesson.Lesson.ArticleIDs = lesson.Lesson.SourceIDs
			return Usage{TotalTokens: 10}, nil
		}
		if m.lessonCalls == 2 && !strings.Contains(prompt, "Copy article_ids only from packet.draft.articles[].id") {
			return Usage{}, errors.New("retry omitted reference correction instructions")
		}
	}
	return m.stagedWriter.Generate(ctx, prompt, in, out)
}

func TestLessonReferenceCorrectionBeforeDependents(t *testing.T) {
	for _, permanent := range []bool{false, true} {
		e, input, _ := repairFixture(t)
		m := &lessonReferenceWriter{stagedWriter: stagedWriter{draft: e.Content}, permanent: permanent}
		cache := &CachedModel{Model: m, Directory: t.TempDir(), Identity: "test"}
		var draft content.Draft
		_, err := GenerateDraft(context.Background(), cache, input, &draft, nil)
		if permanent {
			var invalid *StageValidationError
			if !errors.As(err, &invalid) || m.lessonCalls != 2 || m.quiz != 0 || m.radio != 0 {
				t.Fatalf("invalid lesson reached dependents: calls=%d quiz=%d radio=%d err=%v", m.lessonCalls, m.quiz, m.radio, err)
			}
		} else {
			if err != nil {
				t.Fatal(err)
			}
			if m.lessonCalls != 3 || m.quiz != 2 {
				t.Fatalf("correction or dependent stages missing: lessons=%d quiz=%d", m.lessonCalls, m.quiz)
			}
			if err := content.ValidateDraft(draft, e.Sources); err != nil {
				t.Fatal(err)
			}
		}
		entries, err := os.ReadDir(cache.Directory)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			var saved stageCheckpoint
			if err := store.Read(filepath.Join(cache.Directory, entry.Name()), &saved); err != nil {
				t.Fatal(err)
			}
			var out lessonOutput
			if err := json.Unmarshal([]byte(saved.Output), &out); err != nil {
				t.Fatal(err)
			}
			if out.Lesson.ID == "" {
				continue
			}
			packet := lessonWritingInput{Packet: verificationSubset(input, e.Content, out.Lesson.ArticleIDs)}
			if err := validStage(packet, &out); err != nil {
				t.Fatalf("invalid lesson cached: %v", err)
			}
		}
	}
}

func TestLegacyLessonWithSourceIDsAsArticlesIsRegenerated(t *testing.T) {
	e, input, _ := repairFixture(t)
	packet := lessonWritingInput{Packet: verificationSubset(input, e.Content, e.Content.Lessons[0].ArticleIDs)}
	m := &lessonContractModel{lesson: e.Content.Lessons[0], calls: 1}
	cache := &CachedModel{Model: m, Directory: t.TempDir(), Identity: "test"}
	var out lessonOutput
	// The previous anonymous input must retain the same serialized cache key.
	legacyInput := struct {
		Packet  VerificationInput `json:"packet"`
		Earlier []string          `json:"earlier_lesson_titles"`
	}{packet.Packet, packet.Earlier}
	keyBytes, err := json.Marshal(struct {
		Identity, Prompt string
		Input            any
		Schema           map[string]any
	}{cache.Identity, "write", legacyInput, outputSchema(legacyInput, &out)})
	if err != nil {
		t.Fatal(err)
	}
	key := content.Hash(keyBytes)
	out.Lesson = m.lesson
	out.Lesson.ArticleIDs = out.Lesson.SourceIDs
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(cache.Directory, key+".json")
	if err := store.JSON(path, stageCheckpoint{Key: key, Output: string(raw)}); err != nil {
		t.Fatal(err)
	}
	for run := 0; run < 2; run++ {
		if _, err := cache.Generate(context.Background(), "write", packet, &out); err != nil {
			t.Fatal(err)
		}
		if m.calls != 2 || out.Lesson.ArticleIDs[0] != m.lesson.ArticleIDs[0] {
			t.Fatal("invalid checkpoint reused or regenerated lesson not cached")
		}
	}
	var saved stageCheckpoint
	if err := store.Read(path, &saved); err != nil {
		t.Fatal(err)
	}
	if err := content.Decode(strings.NewReader(saved.Output), &out); err != nil {
		t.Fatal(err)
	}
	if err := validStage(packet, &out); err != nil {
		t.Fatalf("legacy checkpoint not replaced: %v", err)
	}
}
