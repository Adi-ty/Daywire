package editorial

import (
	"fmt"
	"strings"
	"time"

	"github.com/Adi-ty/daywire/internal/content"
)

// Show metadata belongs to the application, not to the factual story writer.
func frameRadio(segment content.Segment, date string, position, total int) content.Segment {
	if position == 1 {
		day, err := time.Parse("2006-01-02", date)
		if err == nil {
			segment.Text = fmt.Sprintf("Welcome to Daywire. It's %s %s, %s. %s", day.Month(), spokenNumber(day.Day()), spokenNumber(day.Year()), segment.Text)
		}
	}
	if position == total {
		segment.Text += " That's Daywire. Thanks for listening."
	}
	return segment
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
