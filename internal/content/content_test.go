package content

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"reflect"
	"testing"
)

func demo(t *testing.T) Edition {
	t.Helper()
	data, err := os.ReadFile("../../testdata/demo/2026-10-23.json")
	if err != nil {
		t.Fatal(err)
	}
	var e Edition
	if err := Decode(bytes.NewReader(data), &e); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestDemoAndSchemaContract(t *testing.T) {
	e := demo(t)
	if err := Validate(e); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../../schemas/edition.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var saved any
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	generated, _ := json.Marshal(Schema(Edition{}))
	var normalized any
	json.Unmarshal(generated, &normalized)
	if !reflect.DeepEqual(saved, normalized) {
		t.Fatal("schema drift: run daywire schema --out schemas/edition.schema.json")
	}
}

func TestRejectInvalidEditions(t *testing.T) {
	tests := map[string]func(*Edition){
		"unknown source":     func(e *Edition) { e.Content.Articles[0].SourceIDs = []string{"invented"} },
		"duplicate id":       func(e *Edition) { e.Content.Lessons[0].ID = e.Content.Articles[0].ID },
		"invalid answer":     func(e *Edition) { e.Content.Quiz[0].Answer = 10 },
		"duplicate options":  func(e *Edition) { e.Content.Quiz[0].Options[1] = e.Content.Quiz[0].Options[0] },
		"stale audio":        func(e *Edition) { e.Content.Segments[0].Text += " changed" },
		"incomplete lessons": func(e *Edition) { e.Content.Lessons = e.Content.Lessons[:1] },
		"unsafe URL":         func(e *Edition) { e.Sources[0].URL = "javascript:alert(1)" },
		"unknown section":    func(e *Edition) { e.Content.Articles[0].Section = "Other" },
		"bad audio": func(e *Edition) {
			url := "https://media.example.com/a.mp3"
			duration := 301.0
			e.Radio.Status = "ready"
			e.Radio.AudioURL = &url
			e.Radio.DurationSeconds = &duration
		},
		"nonfinite audio": func(e *Edition) {
			url := "https://media.example.com/a.mp3"
			duration := math.NaN()
			e.Radio.Status = "ready"
			e.Radio.AudioURL = &url
			e.Radio.DurationSeconds = &duration
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			e := demo(t)
			mutate(&e)
			if err := Validate(e); err == nil {
				t.Fatal("invalid edition accepted")
			}
		})
	}
}

func TestDecodeRejectsUnknownAndTrailingJSON(t *testing.T) {
	for _, raw := range []string{`{"extra":true}`, `{} {}`, `{"id":"q","lesson_id":"l","prompt":"Question","options":["a","b"],"explanation":"Answer"}`} {
		var q Question
		if Decode(bytes.NewBufferString(raw), &q) == nil {
			t.Fatal("accepted", raw)
		}
	}
}

func TestOptionalEnrichmentStillRequiresValidReferences(t *testing.T) {
	for _, scenario := range []string{"no enrichment", "one lesson", "four questions", "too many lessons", "too many questions", "orphan question"} {
		t.Run(scenario, func(t *testing.T) {
			e := demo(t)
			valid := true
			switch scenario {
			case "no enrichment":
				e.Content.Lessons = []Lesson{}
				e.Content.Quiz = []Question{}
			case "one lesson":
				e.Content.Lessons = e.Content.Lessons[:1]
				questions := []Question{}
				for _, q := range e.Content.Quiz {
					if q.LessonID == e.Content.Lessons[0].ID {
						questions = append(questions, q)
					}
				}
				e.Content.Quiz = questions
			case "four questions":
				e.Content.Quiz = e.Content.Quiz[:4]
			case "too many lessons":
				e.Content.Lessons = append(e.Content.Lessons, e.Content.Lessons[0])
				valid = false
			case "too many questions":
				e.Content.Quiz = append(e.Content.Quiz, e.Content.Quiz[0])
				valid = false
			case "orphan question":
				e.Content.Lessons = []Lesson{}
				valid = false
			}
			if err := Validate(e); (err == nil) != valid {
				t.Fatalf("valid=%v err=%v", valid, err)
			}
		})
	}
}
