package collect

import (
	"context"
	"errors"
	"github.com/Adi-ty/daywire/internal/content"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestPartialCollectionFiltersOldPublicationDates(t *testing.T) {
	until := time.Date(2026, 10, 23, 0, 30, 0, 0, time.UTC)
	client := &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "failed") {
			return nil, errors.New("unavailable")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`<rss><channel><item><title>Recent</title><link>https://example.com/new?utm_source=x</link><pubDate>Thu, 22 Oct 2026 10:00:00 +0000</pubDate><description>new article</description></item><item><title>Old</title><link>https://example.com/old</link><pubDate>Thu, 22 Oct 2020 10:00:00 +0000</pubDate></item></channel></rss>`)), Header: http.Header{}}, nil
	})}
	r, err := (Collector{Client: client}).Run(context.Background(), []Config{{ID: "ok", Name: "Feed", Kind: "rss", URL: "https://example.com/feed"}, {ID: "failed", Name: "Failed", Kind: "rss", URL: "https://example.com/failed"}}, until.Add(-24*time.Hour), until)
	if err != nil || len(r.Candidates) != 1 || len(r.Unavailable) != 1 {
		t.Fatalf("%+v %v", r, err)
	}
	if r.Candidates[0].Source.URL != "https://example.com/new" {
		t.Fatal("URL not normalized")
	}
}

func TestCancellationJoinsRequests(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var active atomic.Int32
	client := &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		active.Add(1)
		defer active.Add(-1)
		cancel()
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}
	configs := []Config{}
	for i := 0; i < 12; i++ {
		configs = append(configs, Config{ID: "feed", Name: "Feed", Kind: "rss", URL: "https://example.com/feed"})
	}
	_, err := (Collector{Client: client}).Run(ctx, configs, time.Now().Add(-24*time.Hour), time.Now())
	if !errors.Is(err, context.Canceled) || active.Load() != 0 {
		t.Fatalf("err=%v active=%d", err, active.Load())
	}
}

func TestPlainRemovesExecutableAndNavigationBlocks(t *testing.T) {
	got := Plain(`<nav>menu</nav><script>steal()</script><p>A &amp; B</p><p>Useful text.</p>`)
	if got != "A & B Useful text." {
		t.Fatal(got)
	}
}

func TestFeedOrderUsesPublicationInsteadOfFetchTime(t *testing.T) {
	now := time.Now()
	old, newer := now.Add(-12*time.Hour), now.Add(-time.Hour)
	items := []Candidate{{Source: content.Source{URL: "https://example.com/old", PublishedAt: &old, DiscoveredAt: now}}, {Source: content.Source{URL: "https://example.com/new", PublishedAt: &newer, DiscoveredAt: now.Add(-time.Hour)}}}
	sortCandidates(items)
	if items[0].Source.URL != "https://example.com/new" {
		t.Fatal("fetch order outranked publication date")
	}
}
func TestSecurityCatchupAndPublisherDiversity(t *testing.T) {
	until := time.Date(2026, 10, 23, 0, 30, 0, 0, time.UTC)
	client := &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		feed := `<rss><channel><item><title>Security advisory: critical vulnerability</title><link>https://example.com/advisory</link><pubDate>Wed, 21 Oct 2026 12:00:00 +0000</pubDate></item><item><title>Old unrelated post</title><link>https://example.com/unrelated</link><pubDate>Wed, 21 Oct 2026 12:00:00 +0000</pubDate></item><item><title>Very old CVE</title><link>https://example.com/expired</link><pubDate>Wed, 14 Oct 2026 12:00:00 +0000</pubDate></item></channel></rss>`
		if r.URL.Path == "/general" {
			feed = `<rss><channel><item><title>New A</title><link>https://example.com/new-a</link><pubDate>Thu, 22 Oct 2026 18:00:00 +0000</pubDate></item><item><title>New B</title><link>https://example.com/new-b</link><pubDate>Thu, 22 Oct 2026 17:00:00 +0000</pubDate></item></channel></rss>`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(feed)), Header: http.Header{}}, nil
	})}
	configs := []Config{{ID: "security", Name: "Security", Kind: "rss", URL: "https://example.com/security", Security: true}, {ID: "general", Name: "General", Kind: "rss", URL: "https://example.com/general"}}
	report, err := (Collector{Client: client}).Run(context.Background(), configs, until.Add(-24*time.Hour), until)
	if err != nil || len(report.Candidates) != 3 {
		t.Fatalf("%+v %v", report, err)
	}
	if !report.Candidates[0].CatchUp || !report.Candidates[0].Priority || report.Candidates[1].Source.Name != "General" {
		t.Fatalf("bad selection: %+v", report.Candidates)
	}
}
