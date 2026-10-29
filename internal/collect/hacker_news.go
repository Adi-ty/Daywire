package collect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Adi-ty/daywire/internal/content"
	"github.com/Adi-ty/daywire/internal/fetch"
)

// Four joined workers keep the wider discovery scan bounded. A missing item
// does not discard the rest of the feed; parent cancellation still stops all work.
func (c Collector) hackerNewsItems(ctx context.Context, cfg Config, ids []int) ([]Candidate, error) {
	type result struct {
		item Candidate
		err  error
	}
	results := make([]result, len(ids))
	jobs := make(chan int, len(ids))
	for i := range ids {
		jobs <- i
	}
	close(jobs)
	var wg sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				if ctx.Err() != nil {
					return
				}
				id := ids[index]
				raw, err := fetch.Get(ctx, c.Client, fmt.Sprintf("https://hacker-news.firebaseio.com/v0/item/%d.json", id), 128<<10)
				if err != nil {
					results[index].err = err
					continue
				}
				var story struct {
					Title, URL, Type string
					Time             int64
					Score            int
					Descendants      int
					Dead, Deleted    bool
				}
				if err := json.Unmarshal(raw, &story); err != nil {
					results[index].err = err
					continue
				}
				if story.Type != "story" || story.Dead || story.Deleted || story.URL == "" {
					continue
				}
				discussion := fmt.Sprintf("https://news.ycombinator.com/item?id=%d", id)
				item := makeCandidate(cfg, story.Title, story.URL, "", nil, time.Unix(story.Time, 0), &discussion)
				item.Score = max(0, story.Score)
				item.HN = &content.HNEngagement{Points: item.Score, Comments: max(0, story.Descendants)}
				results[index].item = item
			}
		}()
	}
	wg.Wait()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	items, failures := []Candidate{}, 0
	for _, result := range results {
		if result.err != nil {
			failures++
		}
		if result.item.Source.ID != "" {
			items = append(items, result.item)
		}
	}
	if failures > 0 && len(items) == 0 {
		return nil, errors.New("no Hacker News items could be retrieved")
	}
	return items, nil
}
