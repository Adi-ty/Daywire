package editorial

import (
	"fmt"
	"regexp"
)

var editorialID = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

// IDs are application-owned routing keys, not editorial claims. Preserve a
// usable model suggestion for checkpoint compatibility; resolve collisions
// before review and before dependent stages receive the new object's ID.
func newIdentityAllocator() func(kind, suggested string) string {
	used := map[string]bool{"edition": true}
	return func(kind, suggested string) string {
		id := suggested
		if !editorialID.MatchString(id) || used[id] {
			for n := 1; ; n++ {
				id = fmt.Sprintf("%s-%d", kind, n)
				if !used[id] {
					break
				}
			}
		}
		used[id] = true
		return id
	}
}
