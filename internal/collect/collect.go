package collect

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Adi-ty/daywire/internal/content"
	"github.com/Adi-ty/daywire/internal/fetch"
)

type Config struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	URL      string `json:"url"`
	Security bool   `json:"security,omitempty"`
}
type Candidate struct {
	Source   content.Source `json:"source"`
	Excerpt  string         `json:"excerpt"`
	CatchUp  bool           `json:"catch_up,omitempty"`
	Priority bool           `json:"priority,omitempty"`
}
type Report struct {
	Candidates  []Candidate `json:"candidates"`
	Unavailable []string    `json:"unavailable"`
}
type Collector struct{ Client *http.Client }

// Run uses bounded workers and preserves partial source failures. Cancellation
// is fatal and waits for workers; no goroutine outlives this call.
func (c Collector) Run(ctx context.Context, configs []Config, from, until time.Time) (Report, error) {
	jobs := make(chan Config)
	type result struct {
		id    string
		items []Candidate
		err   error
	}
	results := make(chan result)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(jobs)
		for _, cfg := range configs {
			select {
			case jobs <- cfg:
			case <-ctx.Done():
				return
			}
		}
	}()
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case cfg, ok := <-jobs:
					if !ok || ctx.Err() != nil {
						return
					}
					task, cancel := context.WithTimeout(ctx, 90*time.Second)
					items, err := c.read(task, cfg, time.Now().UTC())
					cancel()
					select {
					case results <- result{cfg.ID, items, err}:
					case <-ctx.Done():
						return
					}
				}
			}
		}()
	}
	joined := make(chan struct{})
	go func() { defer close(joined); wg.Wait(); close(results) }()
	securityFeeds := map[string]bool{}
	for _, cfg := range configs {
		securityFeeds[cfg.ID] = cfg.Security
	}
	report := Report{Candidates: []Candidate{}, Unavailable: []string{}}
	for r := range results {
		if r.err != nil {
			report.Unavailable = append(report.Unavailable, r.id)
			continue
		}
		for _, item := range r.items {
			// Community timestamps describe submission, not original publication.
			fresh := item.Source.DiscoveredAt
			if item.Source.PublishedAt != nil {
				fresh = *item.Source.PublishedAt
			}
			start := from
			advisory := securityFeeds[r.id] && securityTitle.MatchString(item.Source.Title)
			if advisory {
				start = until.Add(-72 * time.Hour)
				item.Priority = true
				item.CatchUp = fresh.Before(from)
			}
			if !fresh.Before(start) && !fresh.After(until) {
				report.Candidates = append(report.Candidates, item)
			}
		}
	}
	<-joined
	if ctx.Err() != nil {
		return report, ctx.Err()
	}
	sort.Strings(report.Unavailable)
	sortCandidates(report.Candidates)
	seen := map[string]bool{}
	unique := []Candidate{}
	perPublisher := map[string]int{}
	// First take one candidate per publisher, then a second/third pass. Feed
	// fetch completion order must not decide which publishers reach the model.
	for pass := 0; pass < 6; pass++ {
		for _, item := range report.Candidates {
			if seen[item.Source.URL] || perPublisher[item.Source.Name] != pass {
				continue
			}
			seen[item.Source.URL] = true
			perPublisher[item.Source.Name]++
			unique = append(unique, item)
			if len(unique) == 32 {
				break
			}
		}
		if len(unique) == 32 {
			break
		}
	}
	report.Candidates = unique
	return report, nil
}

var securityTitle = regexp.MustCompile(`(?i)cve[- ]|ghsa[- ]|vulnerabilit|security (?:advisory|release|update)|critical (?:security|remote)`)

func sortCandidates(items []Candidate) {
	sort.Slice(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if a.Priority != b.Priority {
			return a.Priority
		}
		at, bt := a.Source.DiscoveredAt, b.Source.DiscoveredAt
		if a.Source.PublishedAt != nil {
			at = *a.Source.PublishedAt
		}
		if b.Source.PublishedAt != nil {
			bt = *b.Source.PublishedAt
		}
		if at.Equal(bt) {
			return a.Source.URL < b.Source.URL
		}
		return at.After(bt)
	})
}

func makeCandidate(cfg Config, title, link, text string, published *time.Time, discovered time.Time, discussion *string) Candidate {
	link = fetch.Canonical(link)
	return Candidate{Source: content.Source{ID: "source-" + content.Hash([]byte(link))[:16], Name: cfg.Name, Title: Plain(title), URL: link, PublishedAt: published, DiscoveredAt: discovered, DiscussionURL: discussion}, Excerpt: Plain(text)}
}

func (c Collector) read(ctx context.Context, cfg Config, now time.Time) ([]Candidate, error) {
	data, err := fetch.Get(ctx, c.Client, cfg.URL, 2<<20)
	if err != nil {
		return nil, err
	}
	items := []Candidate{}
	switch cfg.Kind {
	case "rss":
		var feed struct {
			XMLName xml.Name
			Channel struct {
				Items []struct {
					Title       string `xml:"title"`
					Link        string `xml:"link"`
					Description string `xml:"description"`
					Encoded     string `xml:"encoded"`
					Date        string `xml:"pubDate"`
				} `xml:"item"`
			} `xml:"channel"`
			Entries []struct {
				Title string `xml:"title"`
				Links []struct {
					Href string `xml:"href,attr"`
					Rel  string `xml:"rel,attr"`
				} `xml:"link"`
				Summary   string `xml:"summary"`
				Content   string `xml:"content"`
				Published string `xml:"published"`
				Updated   string `xml:"updated"`
			} `xml:"entry"`
		}
		if err := xml.Unmarshal(data, &feed); err != nil {
			return nil, err
		}
		if feed.XMLName.Local != "rss" && feed.XMLName.Local != "feed" {
			return nil, errors.New("not an RSS or Atom feed")
		}
		for _, i := range feed.Channel.Items {
			date := parseDate(i.Date)
			if date == nil {
				continue
			}
			text := i.Encoded
			if text == "" {
				text = i.Description
			}
			items = append(items, makeCandidate(cfg, i.Title, i.Link, text, date, now, nil))
		}
		for _, i := range feed.Entries {
			date := parseDate(i.Published)
			if date == nil {
				date = parseDate(i.Updated)
			}
			if date == nil {
				continue
			}
			link := ""
			for _, l := range i.Links {
				if l.Rel == "" || l.Rel == "alternate" {
					link = l.Href
					break
				}
			}
			text := i.Content
			if text == "" {
				text = i.Summary
			}
			items = append(items, makeCandidate(cfg, i.Title, link, text, date, now, nil))
		}
	case "hn":
		var ids []int
		if err := json.Unmarshal(data, &ids); err != nil {
			return nil, err
		}
		if len(ids) > 30 {
			ids = ids[:30]
		}
		for _, id := range ids {
			raw, err := fetch.Get(ctx, c.Client, fmt.Sprintf("https://hacker-news.firebaseio.com/v0/item/%d.json", id), 128<<10)
			if err != nil {
				return nil, err
			}
			var i struct {
				Title, URL, Type string
				Time             int64
				Dead, Deleted    bool
			}
			if err := json.Unmarshal(raw, &i); err != nil {
				return nil, err
			}
			if i.Type != "story" || i.Dead || i.Deleted || i.URL == "" {
				continue
			}
			discussion := fmt.Sprintf("https://news.ycombinator.com/item?id=%d", id)
			items = append(items, makeCandidate(cfg, i.Title, i.URL, "", nil, time.Unix(i.Time, 0), &discussion))
		}
	case "lobsters":
		var entries []struct {
			Title    string    `json:"title"`
			URL      string    `json:"url"`
			Created  time.Time `json:"created_at"`
			Comments string    `json:"comments_url"`
		}
		if err := json.Unmarshal(data, &entries); err != nil {
			return nil, err
		}
		for _, i := range entries {
			discussion := i.Comments
			items = append(items, makeCandidate(cfg, i.Title, i.URL, "", nil, i.Created, &discussion))
		}
	default:
		return nil, fmt.Errorf("unsupported source kind %q", cfg.Kind)
	}
	valid := []Candidate{}
	for _, item := range items {
		if content.WebURL(item.Source.URL) && item.Source.Title != "" {
			valid = append(valid, item)
		}
	}
	return valid, nil
}

func parseDate(raw string) *time.Time {
	for _, layout := range []string{time.RFC3339, time.RFC1123Z, time.RFC1123, time.RFC822Z, time.RFC822, "Mon, 2 Jan 2006 15:04:05 -0700"} {
		if t, err := time.Parse(layout, strings.TrimSpace(raw)); err == nil {
			return &t
		}
	}
	return nil
}

var unsafeBlocks = regexp.MustCompile(`(?is)<(script|style|nav|header|footer|aside)\b[^>]*>.*?</(?:script|style|nav|header|footer|aside)>`)
var tags = regexp.MustCompile(`(?s)<[^>]*>`)
var breaks = regexp.MustCompile(`(?i)</(?:p|div|li|h[1-6]|section|article)>|<br\s*/?>`)
var mainBlock = regexp.MustCompile(`(?is)<(?:article|main)\b[^>]*>(.*?)</(?:article|main)>`)

func Plain(raw string) string {
	raw = unsafeBlocks.ReplaceAllString(raw, "")
	raw = breaks.ReplaceAllString(raw, "\n")
	raw = html.UnescapeString(tags.ReplaceAllString(raw, " "))
	return strings.Join(strings.Fields(raw), " ")
}

// Evidence reads a bounded article excerpt. It never invents a publication date
// for a community submission, and records when only feed text is available.
func (c Collector) Evidence(ctx context.Context, items []Candidate) ([]content.Evidence, []string, error) {
	all := []content.Evidence{}
	failed := []string{}
	for _, item := range items {
		if ctx.Err() != nil {
			return nil, failed, ctx.Err()
		}
		text := item.Excerpt
		kind := "feed_excerpt"
		if len(text) < 600 {
			data, err := fetch.Get(ctx, c.Client, item.Source.URL, 2<<20)
			if err == nil {
				raw := string(data)
				if match := mainBlock.FindStringSubmatch(raw); len(match) > 1 {
					raw = match[1]
				}
				text = Plain(raw)
				kind = "article_excerpt"
			}
		}
		if len(text) < 160 {
			failed = append(failed, item.Source.ID)
			continue
		}
		runes := []rune(text)
		if len(runes) > 10000 {
			text = string(runes[:10000])
		}
		if item.CatchUp {
			kind = "security_catchup_" + kind
		}
		all = append(all, content.Evidence{Source: item.Source, Text: text, Hash: content.Hash([]byte(text)), Kind: kind})
		if len(all) == 12 {
			break
		}
	}
	return all, failed, nil
}
