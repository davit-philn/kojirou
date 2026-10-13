package mangadex

import (
	"net/url"
	"regexp"
	"strings"
)

var seriesUUID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// ParseSeriesID extracts a series ID from a bare ID or from a MangaDex
// title link such as https://mangadex.org/title/<id>/<slug>. It reports
// false if s is neither.
func ParseSeriesID(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if seriesUUID.MatchString(s) {
		return strings.ToLower(s), true
	}

	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "", false
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	if host != "mangadex.org" {
		return "", false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) >= 2 && parts[0] == "title" && seriesUUID.MatchString(parts[1]) {
		return strings.ToLower(parts[1]), true
	}

	return "", false
}
