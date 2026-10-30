package editorial

import (
	"fmt"
	"strings"
	"time"

	"github.com/Adi-ty/daywire/internal/content"
)

// Show metadata belongs to the application, not to the factual story writer.
func frameRadio(segment content.Segment, date string, position, total int) content.Segment {
	prefix, suffix := radioFraming(date, position, total)
	segment.Text = prefix + segment.Text + suffix
	return segment
}

func radioFraming(date string, position, total int) (prefix, suffix string) {
	if position == 1 {
		day, err := time.Parse("2006-01-02", date)
		if err == nil {
			prefix = fmt.Sprintf("Hello everyone, and welcome to Daywire. Let's make sense of the tech news. It's %s %s, %s.\n\nWe begin with our lead story. ", day.Month(), spokenNumber(day.Day()), spokenNumber(day.Year()))
		}
	}
	if position > 1 {
		transition := "Next on the bulletin. "
		if position == total {
			transition = "And finally, our last story. "
		} else if position%2 == 1 {
			transition = "Turning to our next story. "
		}
		prefix = transition
	}
	if position == total {
		suffix = "\n\nAnd that's your Daywire briefing. You'll find the full stories and sources in today's newspaper. Thanks for listening. Take care, everyone."
	}
	return prefix, suffix
}

// Strip only the exact application-owned frame for this date and position.
// This preserves the factual body's review cache after assembling the show.
// Wrong dates, altered handoffs, and any added claims remain visible to review.
func radioReviewBody(text, date string, position, total int) string {
	prefix, suffix := radioFraming(date, position, total)
	if prefix == "" || !strings.HasPrefix(text, prefix) || !strings.HasSuffix(text, suffix) || len(text) < len(prefix)+len(suffix) {
		return text
	}
	return strings.TrimSuffix(strings.TrimPrefix(text, prefix), suffix)
}

func spokenNumber(n int) string {
	small := []string{"zero", "one", "two", "three", "four", "five", "six", "seven", "eight", "nine", "ten", "eleven", "twelve", "thirteen", "fourteen", "fifteen", "sixteen", "seventeen", "eighteen", "nineteen"}
	if n < 20 {
		return small[n]
	}
	if n < 100 {
		tens := []string{"", "", "twenty", "thirty", "forty", "fifty", "sixty", "seventy", "eighty", "ninety"}
		if n%10 == 0 {
			return tens[n/10]
		}
		return tens[n/10] + " " + small[n%10]
	}
	if n < 1000 {
		if n%100 == 0 {
			return small[n/100] + " hundred"
		}
		return small[n/100] + " hundred " + spokenNumber(n%100)
	}
	if n%1000 == 0 {
		return spokenNumber(n/1000) + " thousand"
	}
	return spokenNumber(n/1000) + " thousand " + spokenNumber(n%1000)
}

func radioBodyError(s string) error {
	lower := strings.ToLower(s)
	for _, forbidden := range []string{"welcome to", "thanks for listening", "thank you for listening", "this was daywire", "that's daywire", "this is daywire"} {
		if strings.Contains(lower, forbidden) {
			return &StageValidationError{"radio body must omit show greetings, broadcast dates and sign-offs; the application adds them"}
		}
	}
	return nil
}
