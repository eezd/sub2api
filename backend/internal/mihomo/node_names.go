package mihomo

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/Wei-Shaw/sub2api/internal/util/logredact"
)

var nodeNameURL = regexp.MustCompile(`(?i)[a-z][a-z0-9+.-]*://[^\s]+`)

// Display labels are never used as controller targets or state keys.
func sanitizeNodeDisplayName(name string) string {
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, name)
	name = nodeNameURL.ReplaceAllString(name, "[redacted URL]")
	name = logredact.RedactText(name, "token", "secret", "uuid", "authorization", "subscription")
	runes := []rune(strings.TrimSpace(name))
	if len(runes) > 120 {
		runes = runes[:120]
	}
	return string(runes)
}
