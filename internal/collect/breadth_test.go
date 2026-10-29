package collect

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Adi-ty/daywire/internal/content"
)

func TestHackerNewsReadsBeyondThirtyAndSurvivesMissingItem(t *testing.T) {
	var active, peak, calls atomic.Int32
	client := &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		count := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); count > old; old = peak.Load() {
			if peak.CompareAndSwap(old, count) {
				break
			}
		}
		calls.Add(1)
		if strings.Contains(r.URL.Path, "/40.json") {
			return &http.Response{StatusCode: 404, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("missing"))}, nil
		}
		text, _ := json.Marshal(map[string]any{"type": "story", "title": "A useful development", "url": "https://publisher.example" + r.URL.Path, "time": 1792886400, "score": 25, "descendants": 80})
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(text)))}, nil
	})}
	ids := []int{}
	for i := 1; i <= 100; i++ {
		ids = append(ids, i)
	}
	items, err := (Collector{Client: client}).hackerNewsItems(context.Background(), Config{ID: "hn", Name: "Hacker News"}, ids)
	if err != nil || len(items) != 99 || calls.Load() != 100 || peak.Load() > 4 || active.Load() != 0 {
		t.Fatalf("items=%d calls=%d active=%d peak=%d err=%v", len(items), calls.Load(), active.Load(), peak.Load(), err)
	}
	if items[len(items)-1].Score != 25 || items[len(items)-1].HN.Comments != 80 || items[len(items)-1].Source.PublishedAt != nil {
		t.Fatal("discovery metadata lost")
	}
}

func TestEvidencePoolIsBroaderAndRetainsParagraphs(t *testing.T) {
	items := []Candidate{}
	for i := 0; i < 48; i++ {
		items = append(items, Candidate{HN: &content.HNEngagement{Points: 300, Comments: 100}, Source: content.Source{ID: fmt.Sprintf("source-%d", i)}, Excerpt: paragraphs("<p>" + strings.Repeat("A supported introduction. ", 100) + "</p><p>However, this is an experimental release.</p>")})
	}
	evidence, failed, err := (Collector{}).Evidence(context.Background(), items)
	if err != nil || len(failed) != 0 || len(evidence) != 36 || evidence[0].HN.Points != 300 || evidence[0].HN.Comments != 100 || !strings.Contains(evidence[0].Text, "\n\nHowever") {
		t.Fatalf("evidence=%d failed=%v err=%v", len(evidence), failed, err)
	}
}

func TestCutoffBelongsToNextEdition(t *testing.T) {
	until := time.Date(2026, 10, 26, 0, 30, 0, 0, time.UTC)
	client := &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		body := `<rss><channel><item><title>At cutoff</title><link>https://example.com/cutoff</link><pubDate>Mon, 26 Oct 2026 00:30:00 +0000</pubDate></item><item><title>At start</title><link>https://example.com/start</link><pubDate>Sun, 25 Oct 2026 00:30:00 +0000</pubDate></item></channel></rss>`
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	report, err := (Collector{Client: client}).Run(context.Background(), []Config{{ID: "test", Name: "Test", Kind: "rss", URL: "https://example.com/feed"}}, until.Add(-24*time.Hour), until)
	if err != nil || len(report.Candidates) != 1 || report.Candidates[0].Source.Title != "At start" {
		t.Fatalf("%+v %v", report, err)
	}
}

func TestHNCommentsCannotOutweighSubstantiallyMorePoints(t *testing.T) {
	items := []Candidate{
		{HN: &content.HNEngagement{Points: 20, Comments: 2000}},
		{HN: &content.HNEngagement{Points: 300, Comments: 20}},
		{HN: &content.HNEngagement{Points: 300, Comments: 100}},
	}
	sortCandidates(items)
	if items[0].HN.Comments != 100 || items[1].HN.Points != 300 || items[2].HN.Points != 20 {
		t.Fatal("points should dominate, comments break close ties")
	}
}

func TestDuplicatePrimaryFeedRetainsHNEngagement(t *testing.T) {
	until := time.Date(2026, 10, 29, 0, 30, 0, 0, time.UTC)
	client := &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		var body string
		switch r.URL.Path {
		case "/feed":
			body = `<rss><channel><item><title>Security update</title><link>https://example.com/story</link><pubDate>Wed, 28 Oct 2026 12:00:00 +0000</pubDate></item></channel></rss>`
		case "/topstories.json":
			body = "[1]"
		default:
			raw, _ := json.Marshal(map[string]any{"type": "story", "title": "Security update", "url": "https://example.com/story", "time": until.Add(-12 * time.Hour).Unix(), "score": 600, "descendants": 250})
			body = string(raw)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	report, err := (Collector{Client: client}).Run(context.Background(), []Config{
		{ID: "primary", Name: "Primary", Kind: "rss", URL: "https://example.com/feed", Security: true},
		{ID: "hn", Name: "Hacker News", Kind: "hn", URL: "https://example.com/topstories.json"},
	}, until.Add(-24*time.Hour), until)
	if err != nil || len(report.Candidates) != 1 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	item := report.Candidates[0]
	if item.Source.Name != "Primary" || item.HN == nil || item.HN.Points != 600 || item.HN.Comments != 250 {
		t.Fatalf("primary source lost discovery signals: %+v", item)
	}
}
