// Package content defines the versioned boundary between generation and the website.
package content

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"time"
)

const Version = "1"

type Source struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	Title         string     `json:"title"`
	URL           string     `json:"url"`
	PublishedAt   *time.Time `json:"published_at"`
	DiscoveredAt  time.Time  `json:"discovered_at"`
	DiscussionURL *string    `json:"discussion_url"`
}

// Evidence stays in private run state; only Source metadata enters the website.
type Evidence struct {
	Source Source `json:"source"`
	Text   string `json:"text"`
	Hash   string `json:"hash"`
	Kind   string `json:"kind"`
}

type Article struct {
	ID          string   `json:"id"`
	Section     string   `json:"section"`
	Headline    string   `json:"headline"`
	Summary     string   `json:"summary"`
	Paragraphs  []string `json:"paragraphs"`
	Implication string   `json:"implication"`
	SourceIDs   []string `json:"source_ids"`
}

type Lesson struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Paragraphs []string `json:"paragraphs"`
	Example    string   `json:"example"`
	Takeaway   string   `json:"takeaway"`
	ArticleIDs []string `json:"article_ids"`
	SourceIDs  []string `json:"source_ids"`
}

type Question struct {
	ID          string   `json:"id"`
	LessonID    string   `json:"lesson_id"`
	Prompt      string   `json:"prompt"`
	Options     []string `json:"options"`
	Answer      int      `json:"answer"`
	Explanation string   `json:"explanation"`
}

type Segment struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Text       string   `json:"text"`
	ArticleIDs []string `json:"article_ids"`
}

type Draft struct {
	Title        string     `json:"title"`
	Introduction string     `json:"introduction"`
	Articles     []Article  `json:"articles"`
	Lessons      []Lesson   `json:"lessons"`
	Quiz         []Question `json:"quiz"`
	Segments     []Segment  `json:"segments"`
	ArtworkBrief string     `json:"artwork_brief"`
}

type Coverage struct {
	From               time.Time `json:"from"`
	Until              time.Time `json:"until"`
	Status             string    `json:"status"`
	UnavailableSources []string  `json:"unavailable_sources"`
	CandidateCount     int       `json:"candidate_count"`
	EvidenceCount      int       `json:"evidence_count"`
}

type Radio struct {
	Status          string          `json:"status"`
	AudioURL        *string         `json:"audio_url"`
	DurationSeconds *float64        `json:"duration_seconds"`
	ScriptHash      string          `json:"script_hash"`
	Cues            []TranscriptCue `json:"cues,omitempty"`
}

// TranscriptCue is a word aligned to the actual narration, not an estimated
// reading rate. Omitted cues preserve compatibility with older recordings.
type TranscriptCue struct {
	SegmentID    string  `json:"segment_id"`
	Text         string  `json:"text"`
	StartSeconds float64 `json:"start_seconds"`
	EndSeconds   float64 `json:"end_seconds"`
}

type Artwork struct {
	Status string  `json:"status"`
	URL    *string `json:"url"`
	Alt    string  `json:"alt"`
}

type Edition struct {
	SchemaVersion string    `json:"schema_version"`
	Date          string    `json:"date"`
	Timezone      string    `json:"timezone"`
	Revision      int       `json:"revision"`
	Demo          bool      `json:"demo"`
	GeneratedAt   time.Time `json:"generated_at"`
	Coverage      Coverage  `json:"coverage"`
	Content       Draft     `json:"content"`
	Sources       []Source  `json:"sources"`
	Radio         Radio     `json:"radio"`
	Artwork       Artwork   `json:"artwork"`
}

func Hash(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

func Decode(r io.Reader, target any) error {
	d := json.NewDecoder(r)
	var raw json.RawMessage
	if err := d.Decode(&raw); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return errors.New("expected exactly one JSON value")
	}
	t := reflect.TypeOf(target)
	if t == nil || t.Kind() != reflect.Pointer {
		return errors.New("JSON target must be a pointer")
	}
	if err := requiredFields(raw, t.Elem(), "root"); err != nil {
		return err
	}
	d = json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	return d.Decode(target)
}

// encoding/json alone treats absent required fields as Go zero values. Check
// presence and nullability first so a missing answer cannot become option zero.
func requiredFields(raw json.RawMessage, t reflect.Type, path string) error {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		if t.Kind() == reflect.Pointer {
			return nil
		}
		return fmt.Errorf("%s cannot be null", path)
	}
	if t.Kind() == reflect.Pointer {
		return requiredFields(raw, t.Elem(), path)
	}
	if t == reflect.TypeOf(time.Time{}) {
		return nil
	}
	switch t.Kind() {
	case reflect.Struct:
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			return err
		}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			name := strings.Split(f.Tag.Get("json"), ",")[0]
			if name == "-" {
				continue
			}
			if name == "" {
				name = f.Name
			}
			value, ok := fields[name]
			if !ok {
				if strings.Contains(f.Tag.Get("json"), ",omitempty") {
					continue
				}
				return fmt.Errorf("missing required field %s.%s", path, name)
			}
			if err := requiredFields(value, f.Type, path+"."+name); err != nil {
				return err
			}
		}
	case reflect.Slice:
		var values []json.RawMessage
		if err := json.Unmarshal(raw, &values); err != nil {
			return err
		}
		for i, v := range values {
			if err := requiredFields(v, t.Elem(), fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	}
	return nil
}

func WebURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil
}

func (d Draft) Script() string {
	parts := make([]string, 0, len(d.Segments))
	for _, s := range d.Segments {
		parts = append(parts, s.Text)
	}
	return strings.Join(parts, "\n\n")
}

// Targets makes verification exhaustive: an omitted verdict cannot pass silently.
func (d Draft) Targets() []string {
	ids := []string{"edition"}
	for _, a := range d.Articles {
		ids = append(ids, a.ID)
	}
	for _, l := range d.Lessons {
		ids = append(ids, l.ID)
	}
	for _, q := range d.Quiz {
		ids = append(ids, q.ID)
	}
	for _, s := range d.Segments {
		ids = append(ids, s.ID)
	}
	return ids
}

var identifier = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

func ValidateDraft(d Draft, sources []Source) error {
	if strings.TrimSpace(d.Title) == "" || len(d.Title) > 180 || strings.TrimSpace(d.Introduction) == "" || len(d.Introduction) > 1000 {
		return errors.New("invalid edition heading")
	}
	if len(d.Articles) < 1 || len(d.Articles) > 12 {
		return errors.New("edition needs 1–12 articles")
	}
	if len(d.Lessons) != 2 || len(d.Quiz) != 5 {
		return errors.New("edition needs two lessons and five questions")
	}
	if len(d.Segments) < 1 || len(d.Segments) > 10 || len(strings.Fields(d.Script())) > 650 {
		return errors.New("radio needs 1–10 segments and at most 650 words")
	}
	if strings.TrimSpace(d.ArtworkBrief) == "" || len(d.ArtworkBrief) > 2000 {
		return errors.New("invalid artwork brief")
	}
	known := map[string]bool{"edition": true}
	add := func(id string) error {
		if !identifier.MatchString(id) || known[id] {
			return fmt.Errorf("invalid or duplicate ID %q", id)
		}
		known[id] = true
		return nil
	}
	sourceIDs, articleIDs, lessonIDs := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, s := range sources {
		if !identifier.MatchString(s.ID) || sourceIDs[s.ID] || !WebURL(s.URL) || s.Name == "" || s.Title == "" || s.DiscoveredAt.IsZero() {
			return fmt.Errorf("invalid source %q", s.ID)
		}
		if s.DiscussionURL != nil && !WebURL(*s.DiscussionURL) {
			return errors.New("invalid discussion URL")
		}
		sourceIDs[s.ID] = true
	}
	refs := func(ids []string, allowed map[string]bool) bool {
		if len(ids) == 0 {
			return false
		}
		seen := map[string]bool{}
		for _, id := range ids {
			if !allowed[id] || seen[id] {
				return false
			}
			seen[id] = true
		}
		return true
	}
	paragraphs := func(parts []string) bool {
		if len(parts) < 1 || len(parts) > 8 {
			return false
		}
		for _, p := range parts {
			if strings.TrimSpace(p) == "" || len(p) > 3000 {
				return false
			}
		}
		return true
	}
	headlines := map[string]bool{}
	for _, a := range d.Articles {
		if err := add(a.ID); err != nil {
			return err
		}
		if a.Section != "AI" && a.Section != "Engineering" && a.Section != "Security" && a.Section != "Tools" {
			return fmt.Errorf("unknown section %q", a.Section)
		}
		h := strings.ToLower(strings.TrimSpace(a.Headline))
		if h == "" || headlines[h] || len(a.Headline) > 200 || a.Summary == "" || len(a.Summary) > 1400 || a.Implication == "" || len(a.Implication) > 1500 || !paragraphs(a.Paragraphs) || !refs(a.SourceIDs, sourceIDs) {
			return fmt.Errorf("invalid article %s", a.ID)
		}
		headlines[h], articleIDs[a.ID] = true, true
	}
	for _, l := range d.Lessons {
		if err := add(l.ID); err != nil {
			return err
		}
		if err := ValidateLessonText(l); err != nil {
			return fmt.Errorf("lesson %s: %w", l.ID, err)
		}
		if !refs(l.ArticleIDs, articleIDs) || !refs(l.SourceIDs, sourceIDs) {
			return fmt.Errorf("invalid lesson %s", l.ID)
		}
		lessonIDs[l.ID] = true
	}
	for _, q := range d.Quiz {
		if err := add(q.ID); err != nil {
			return err
		}
		if !lessonIDs[q.LessonID] || q.Prompt == "" || len(q.Prompt) > 1000 || len(q.Options) < 2 || len(q.Options) > 4 || q.Answer < 0 || q.Answer >= len(q.Options) || q.Explanation == "" || len(q.Explanation) > 1500 {
			return fmt.Errorf("invalid question %s", q.ID)
		}
		options := map[string]bool{}
		for _, o := range q.Options {
			key := strings.ToLower(strings.TrimSpace(o))
			if key == "" || len(o) > 600 || options[key] {
				return fmt.Errorf("invalid options in %s", q.ID)
			}
			options[key] = true
		}
	}
	for _, s := range d.Segments {
		if err := add(s.ID); err != nil {
			return err
		}
		if s.Title == "" || len(s.Title) > 160 || strings.TrimSpace(s.Text) == "" || !refs(s.ArticleIDs, articleIDs) {
			return fmt.Errorf("invalid radio segment %s", s.ID)
		}
	}
	return nil
}

func Validate(e Edition) error {
	date, err := time.Parse("2006-01-02", e.Date)
	if err != nil || date.Format("2006-01-02") != e.Date || e.SchemaVersion != Version || e.Timezone != "Asia/Kolkata" || e.Revision < 1 || e.GeneratedAt.IsZero() {
		return errors.New("invalid edition metadata")
	}
	if !e.Coverage.From.Before(e.Coverage.Until) || e.Coverage.Until.After(e.GeneratedAt) || e.Coverage.EvidenceCount < len(e.Sources) || e.Coverage.CandidateCount < e.Coverage.EvidenceCount || e.Coverage.UnavailableSources == nil {
		return errors.New("invalid coverage")
	}
	if e.Coverage.Status != "complete" && e.Coverage.Status != "partial" {
		return errors.New("invalid coverage status")
	}
	if len(e.Coverage.UnavailableSources) > 0 && e.Coverage.Status != "partial" {
		return errors.New("failed sources require partial coverage")
	}
	if err := ValidateDraft(e.Content, e.Sources); err != nil {
		return err
	}
	if e.Radio.ScriptHash != Hash([]byte(e.Content.Script())) {
		return errors.New("radio script hash mismatch")
	}
	switch e.Radio.Status {
	case "pending", "failed":
		if e.Radio.AudioURL != nil || e.Radio.DurationSeconds != nil {
			return errors.New("unavailable audio must not have an asset")
		}
	case "ready":
		if e.Radio.AudioURL == nil || !WebURL(*e.Radio.AudioURL) || e.Radio.DurationSeconds == nil || math.IsNaN(*e.Radio.DurationSeconds) || math.IsInf(*e.Radio.DurationSeconds, 0) || *e.Radio.DurationSeconds <= 0 || *e.Radio.DurationSeconds > 300 {
			return errors.New("invalid ready audio")
		}
	default:
		return errors.New("unknown radio status")
	}
	if e.Artwork.Status == "ready" {
		if e.Artwork.URL == nil || !WebURL(*e.Artwork.URL) || e.Artwork.Alt == "" {
			return errors.New("invalid artwork")
		}
	} else if e.Artwork.Status != "pending" || e.Artwork.URL != nil {
		return errors.New("invalid artwork status")
	}
	if len(e.Radio.Cues) > 0 {
		if e.Radio.Status != "ready" {
			return errors.New("transcript timings require ready audio")
		}
		index := 0
		previousStart := -1.0
		for _, segment := range e.Content.Segments {
			for _, word := range strings.Fields(segment.Text) {
				if index >= len(e.Radio.Cues) {
					return errors.New("incomplete transcript timings")
				}
				cue := e.Radio.Cues[index]
				index++
				if cue.SegmentID != segment.ID || cue.Text != word || math.IsNaN(cue.StartSeconds) || math.IsNaN(cue.EndSeconds) || math.IsInf(cue.StartSeconds, 0) || math.IsInf(cue.EndSeconds, 0) || cue.StartSeconds < 0 || cue.StartSeconds < previousStart || cue.EndSeconds < cue.StartSeconds || cue.EndSeconds > *e.Radio.DurationSeconds+0.25 {
					return errors.New("invalid transcript timing or text")
				}
				previousStart = cue.StartSeconds
			}
		}
		if index != len(e.Radio.Cues) {
			return errors.New("unexpected transcript timing words")
		}
	}
	return nil
}

// ValidateLessonText is shared by the generation boundary and final edition
// validation so an invalid lesson cannot become a premise for quiz generation.
func ValidateLessonText(l Lesson) error {
	for _, f := range []struct {
		name, value string
		limit       int
	}{
		{"title", l.Title, 200}, {"example", l.Example, 3000}, {"takeaway", l.Takeaway, 1000},
	} {
		if strings.TrimSpace(f.value) == "" {
			return fmt.Errorf("%s must not be empty", f.name)
		}
		if len(f.value) > f.limit {
			return fmt.Errorf("%s exceeds %d bytes", f.name, f.limit)
		}
	}
	if len(l.Paragraphs) < 1 || len(l.Paragraphs) > 8 {
		return errors.New("paragraphs must contain 1–8 entries")
	}
	for _, p := range l.Paragraphs {
		if strings.TrimSpace(p) == "" || len(p) > 3000 {
			return errors.New("paragraphs must be nonempty and at most 3000 bytes each")
		}
	}
	return nil
}
