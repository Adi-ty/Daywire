package editorial

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Adi-ty/daywire/internal/content"
)

// Exercise the actual provider schema, decoding, checkpoint and rebinding path.
// The provider returns only editable fields, never identity or provenance.
func TestRepairContentOnlyWireContractAndResume(t *testing.T) {
	for _, kind := range []string{"article", "lesson", "question", "segment"} {
		t.Run(kind, func(t *testing.T) {
			e, input, _ := repairFixture(t)
			var value any
			var id, field string
			switch kind {
			case "article":
				value, id, field = e.Content.Articles[0], e.Content.Articles[0].ID, "summary"
			case "lesson":
				value, id, field = e.Content.Lessons[0], e.Content.Lessons[0].ID, "example"
			case "question":
				value, id, field = e.Content.Quiz[0], e.Content.Quiz[0].ID, "explanation"
			case "segment":
				value, id, field = e.Content.Segments[0], e.Content.Segments[0].ID, "text"
			}
			data, _ := json.Marshal(value)
			var original map[string]any
			if err := json.Unmarshal(data, &original); err != nil {
				t.Fatal(err)
			}
			editable := map[string]any{}
			owned := map[string]bool{"id": true, "source_ids": true, "article_ids": true, "lesson_id": true}
			for key, value := range original {
				if !owned[key] {
					editable[key] = value
				}
			}
			editable[field] = "Corrected content that still requires factual review."
			g := NewGroq("test", "test-model")
			g.wait = func(context.Context, time.Duration) error { return nil }
			calls := 0
			g.Client.Transport = transport(func(r *http.Request) (*http.Response, error) {
				calls++
				var request map[string]any
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Fatal(err)
				}
				schema := request["response_format"].(map[string]any)["json_schema"].(map[string]any)["schema"].(map[string]any)
				object := schema["properties"].(map[string]any)[kind].(map[string]any)
				props := object["properties"].(map[string]any)
				if object["additionalProperties"] != false || len(props) != len(editable) {
					t.Fatal("repair schema must contain only editable fields", object)
				}
				for key := range props {
					if owned[key] {
						t.Fatal("model owns reference field", key)
					}
				}
				for _, key := range object["required"].([]any) {
					if _, exists := props[key.(string)]; !exists {
						t.Fatal("required field missing from schema", key)
					}
				}
				if len(object["required"].([]any)) != len(props) {
					t.Fatal("editable fields must remain required")
				}
				body, _ := json.Marshal(map[string]any{kind: editable})
				response, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": string(body)}, "finish_reason": "stop"}}})
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(response))), Header: http.Header{}}, nil
			})
			cache := &CachedModel{Model: g, Directory: t.TempDir(), Identity: "test"}
			feedback := Review{Verdicts: []Verdict{{id, "contradicted", "Correct the unsupported claim"}}}
			for run := 0; run < 2; run++ {
				fresh, _, _ := repairFixture(t)
				draft := fresh.Content
				if _, err := correctTarget(context.Background(), cache, input, &draft, id, feedback, false); err != nil {
					t.Fatal(err)
				}
				var result any
				switch kind {
				case "article":
					result = draft.Articles[0]
				case "lesson":
					result = draft.Lessons[0]
				case "question":
					result = draft.Quiz[0]
				case "segment":
					result = draft.Segments[0]
				}
				encoded, _ := json.Marshal(result)
				var got map[string]any
				_ = json.Unmarshal(encoded, &got)
				for key, want := range original {
					if key == field {
						want = editable[field]
					}
					if !reflect.DeepEqual(got[key], want) {
						t.Fatalf("field %s changed unexpectedly: %v != %v", key, got[key], want)
					}
				}
				if calls != 1 {
					t.Fatal("resume repeated paid correction", calls)
				}
			}
		})
	}
}

func TestLessonCorrectionPreservesReferencesAndFactualGate(t *testing.T) {
	for _, permanent := range []bool{false, true} {
		name := "supported"
		if permanent {
			name = "rejected"
		}
		t.Run(name, func(t *testing.T) {
			e, input, _ := repairFixture(t)
			id := e.Content.Lessons[1].ID
			for i := 0; i < 3; i++ {
				e.Content.Quiz[i].LessonID = e.Content.Lessons[0].ID
			}
			m := &gatedWriter{stagedWriter: stagedWriter{draft: e.Content}, rejectedID: id, permanent: permanent}
			cache := &CachedModel{Model: m, Directory: t.TempDir(), Identity: "test"}
			var draft content.Draft
			_, report, err := GenerateDraftWithReport(context.Background(), cache, input, &draft, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !m.corrected {
				t.Fatal("correction bypassed review or changed its premises")
			}
			if !reflect.DeepEqual(draft.Articles, e.Content.Articles) {
				t.Fatal("lesson correction changed the newspaper")
			}
			if err := content.ValidateDraft(draft, e.Sources); err != nil {
				t.Fatal(err)
			}
			if permanent {
				if len(draft.Lessons) != 1 || len(report.Omissions) != 1 || report.Omissions[0].TargetID != id {
					t.Fatal("unsupported lesson not omitted", report)
				}
				for _, q := range draft.Quiz {
					if q.LessonID == id {
						t.Fatal("quiz used rejected lesson")
					}
				}
			} else if len(draft.Lessons) != 2 || len(report.Omissions) != 0 || draft.Lessons[1].Example != "Corrected supported example" {
				t.Fatal("supported correction not retained", report)
			}
			calls, reviews := m.calls, m.reviewCalls
			if _, err := GenerateDraft(context.Background(), cache, input, &draft, nil); err != nil {
				t.Fatal(err)
			}
			if _, _, err := Verify(context.Background(), cache, input, draft); err != nil {
				t.Fatal(err)
			}
			if m.calls != calls || m.reviewCalls != reviews {
				t.Fatal("resume repeated completed work or rerolled factual review")
			}
		})
	}
}

// Test fixtures intentionally discard identity fields when simulating an edit.
func copyRepairFields(original, edit any) {
	raw, err := json.Marshal(original)
	if err != nil {
		panic(err)
	}
	if err := json.Unmarshal(raw, edit); err != nil {
		panic(err)
	}
}

func TestRepairDecoderRejectsIdentityAndMissingContent(t *testing.T) {
	for _, raw := range []string{
		`{"segment":{"id":"new-id","title":"Story","text":"Corrected story"}}`,
		`{"segment":{"article_ids":["unrelated"],"title":"Story","text":"Corrected story"}}`,
		`{"segment":{"title":"Story"}}`,
	} {
		var out radioRepairOutput
		if err := content.Decode(strings.NewReader(raw), &out); err == nil {
			t.Fatal("invalid repair contract accepted", raw)
		}
	}
	var question questionRepairOutput
	if err := content.Decode(strings.NewReader(`{"question":{"prompt":"Which?","options":["A","B"],"explanation":"Because"}}`), &question); err == nil {
		t.Fatal("missing answer silently became option zero")
	}
}
