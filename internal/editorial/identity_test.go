package editorial

import (
	"context"
	"testing"

	"github.com/Adi-ty/daywire/internal/content"
)

func TestIdentityAllocator(t *testing.T) {
	for run := 0; run < 2; run++ {
		assign := newIdentityAllocator()
		for _, tc := range []struct{ kind, suggestion, want string }{
			{"article", "story", "story"},
			{"article", "story", "article-1"},
			{"lesson", "story", "lesson-1"},
			{"quiz", "edition", "quiz-1"},
			{"quiz", "quiz-1", "quiz-2"},
			{"radio", "story", "radio-1"},
			{"radio", "radio-1", "radio-2"},
			{"radio", "Bad ID", "radio-3"},
		} {
			if got := assign(tc.kind, tc.suggestion); got != tc.want {
				t.Fatalf("%+v: got %s", tc, got)
			}
		}
	}
}

type collidingRadioWriter struct {
	stagedWriter
	t            *testing.T
	radioReviews int
}

func (m *collidingRadioWriter) Generate(ctx context.Context, system string, in, out any) (Usage, error) {
	if packet, ok := in.(VerificationInput); ok && len(packet.Draft.Segments) > 0 {
		if len(packet.Targets) != 1 || reviewKind(packet.Draft, packet.Targets[0]) != "radio" {
			m.t.Fatal("radio target incorrectly routed to article review")
		}
		for _, article := range packet.Draft.Articles {
			if article.ID == packet.Targets[0] {
				m.t.Fatal("article and narration share an ID")
			}
		}
		m.radioReviews++
	}
	u, err := m.stagedWriter.Generate(ctx, system, in, out)
	if radio, ok := out.(*radioOutput); ok && err == nil {
		// Reproduce the real failure: the writer copies its input article's ID.
		radio.Segment.ID = in.(radioWritingInput).Article.ID
	}
	return u, err
}

func TestCachedRadioArticleCollisionResolvedBeforeReview(t *testing.T) {
	e, input, _ := repairFixture(t)
	writer := &collidingRadioWriter{stagedWriter: stagedWriter{draft: e.Content}, t: t}
	cache := &CachedModel{Model: writer, Directory: t.TempDir(), Identity: "collision-test"}
	var first content.Draft
	if _, err := GenerateDraft(context.Background(), cache, input, &first, nil); err != nil {
		t.Fatal(err)
	}
	if err := content.ValidateDraft(first, e.Sources); err != nil {
		t.Fatal(err)
	}
	if writer.radioReviews != len(first.Segments) {
		t.Fatal("radio bypassed review")
	}
	for i, s := range first.Segments {
		if s.ID == first.Articles[i].ID || s.ArticleIDs[0] != first.Articles[i].ID {
			t.Fatal("identity/reference incorrect")
		}
	}
	calls := writer.calls
	var resumed content.Draft
	if _, err := GenerateDraft(context.Background(), cache, input, &resumed, nil); err != nil {
		t.Fatal(err)
	}
	if writer.calls != calls {
		t.Fatal("cached resume regenerated completed content")
	}
	for i, s := range resumed.Segments {
		if s.ID != first.Segments[i].ID || s.Text != first.Segments[i].Text {
			t.Fatal("resume changed identity or prose")
		}
	}
	if err := content.ValidateDraft(resumed, e.Sources); err != nil {
		t.Fatal(err)
	}
}
