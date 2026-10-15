// Package wikipedia reads the volume tables of Wikipedia's "List of … chapters"
// pages. They say which chapters each tankōbon volume contains, which
// MangaDex often does not know.
package wikipedia

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	md "github.com/leotaku/kojirou/mangadex"
)

const (
	// DefaultAPI is the English Wikipedia API.
	DefaultAPI     = "https://en.wikipedia.org/w/api.php"
	userAgent      = "kojirou (https://github.com/davit-philn/kojirou; volume lists)"
	maxPages       = 3
	maxBodyBytes   = 8 << 20
	requestTimeout = 20 * time.Second
)

// Client talks to the Wikipedia API.
type Client struct {
	api  string
	http *http.Client
}

func New() *Client { return NewWithAPI(DefaultAPI) }

// NewWithAPI uses another API endpoint, e.g. a test server.
func NewWithAPI(api string) *Client {
	return &Client{api: api, http: &http.Client{Timeout: requestTimeout}}
}

// Table says which volume each chapter belongs to.
type Table struct {
	// Page is the title of the Wikipedia page the table comes from.
	Page    string
	Volumes map[md.Identifier]md.Identifier
}

// VolumeTable finds the page listing the chapters of the series and reads its
// volume table. It fails when no page with a usable table is found.
func (c *Client) VolumeTable(ctx context.Context, title string) (*Table, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, fmt.Errorf("no title to search for")
	}

	pages, err := c.search(ctx, title)
	if err != nil {
		return nil, err
	}
	for _, page := range pages {
		text, err := c.wikitext(ctx, page)
		if err != nil {
			return nil, err
		}
		if volumes := ParseVolumes(text, title); len(volumes) > 0 {
			return &Table{Page: page, Volumes: volumes}, nil
		}
	}

	return nil, fmt.Errorf("no Wikipedia page lists the volumes of %q", title)
}

type searchResponse struct {
	Query struct {
		Search []struct {
			Title string `json:"title"`
		} `json:"search"`
	} `json:"query"`
}

var listTitle = regexp.MustCompile(`(?i)^list of .*(chapters|volumes)`)

func (c *Client) search(ctx context.Context, title string) ([]string, error) {
	var res searchResponse
	err := c.get(ctx, url.Values{
		"action": {"query"}, "list": {"search"}, "format": {"json"}, "formatversion": {"2"},
		"srlimit": {"8"}, "srsearch": {"List of " + title + " chapters"},
	}, &res)
	if err != nil {
		return nil, fmt.Errorf("wikipedia search: %w", err)
	}

	// Pages that do not mention the title are other series.
	want := normalize(title)
	var pages []string
	for _, hit := range res.Query.Search {
		if listTitle.MatchString(hit.Title) && strings.Contains(normalize(hit.Title), want) {
			pages = append(pages, hit.Title)
		}
	}
	if len(pages) > maxPages {
		pages = pages[:maxPages]
	}

	return pages, nil
}

type parseResponse struct {
	Parse struct {
		Wikitext string `json:"wikitext"`
	} `json:"parse"`
}

func (c *Client) wikitext(ctx context.Context, page string) (string, error) {
	var res parseResponse
	err := c.get(ctx, url.Values{
		"action": {"parse"}, "page": {page}, "prop": {"wikitext"}, "format": {"json"}, "formatversion": {"2"}, "redirects": {"1"},
	}, &res)
	if err != nil {
		return "", fmt.Errorf("wikipedia page %q: %w", page, err)
	}

	return res.Parse.Wikitext, nil
}

func (c *Client) get(ctx context.Context, query url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", c.api+"?"+query.Encode(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %v", resp.Status)
	}

	return json.NewDecoder(io.LimitReader(resp.Body, maxBodyBytes)).Decode(out)
}

var (
	sectionHeading = regexp.MustCompile(`(?m)^(==[^=\n]+==)[ \t]*$`)
	volumeBlock    = regexp.MustCompile(`\{\{Graphic novel list[ \t]*\n`)
	volumeNumber   = regexp.MustCompile(`\|\s*VolumeNumber\s*=\s*([^\s|}]+)`)
	chapterLine    = regexp.MustCompile(`(?m)^\*\s*(\d+(?:\.\d+)?)\.`)
	blockEnd       = regexp.MustCompile(`(?m)^\}\}`)
	nonWord        = regexp.MustCompile(`[^\p{L}\p{N}]+`)
)

func normalize(s string) string {
	return strings.TrimSpace(nonWord.ReplaceAllString(strings.ToLower(s), " "))
}

// ParseVolumes reads the "Graphic novel list" blocks of a "List of … chapters"
// page into chapter → volume. When the page covers several works (a series and
// its spin-offs) the section named after title is used, or else the one with
// the most volumes.
func ParseVolumes(wikitext, title string) map[md.Identifier]md.Identifier {
	heads := sectionHeading.FindAllStringIndex(wikitext, -1)

	var best map[md.Identifier]md.Identifier
	bestVolumes, bestNamed := 0, false
	for i, h := range heads {
		end := len(wikitext)
		if i+1 < len(heads) {
			end = heads[i+1][0]
		}
		heading := strings.Trim(wikitext[h[0]:h[1]], "= \t")
		volumes, count := parseSection(wikitext[h[1]:end])
		if count == 0 {
			continue
		}
		named := normalize(strings.NewReplacer("'", "", "volumes", "", "chapters", "").Replace(strings.ToLower(heading))) == normalize(title)
		if best == nil || (named && !bestNamed) || (named == bestNamed && count > bestVolumes) {
			best, bestVolumes, bestNamed = volumes, count, named
		}
	}

	return best
}

func parseSection(body string) (map[md.Identifier]md.Identifier, int) {
	blocks := volumeBlock.Split(body, -1)
	result := make(map[md.Identifier]md.Identifier)
	count := 0
	for _, block := range blocks[1:] {
		// The template ends at the first "}}" that starts a line; what
		// follows (e.g. chapters not yet collected) is not in the volume.
		if end := blockEnd.FindStringIndex(block); end != nil {
			block = block[:end[0]]
		}
		m := volumeNumber.FindStringSubmatch(block)
		if m == nil {
			continue
		}
		volume := md.NewIdentifier(m[1])
		if volume.IsSpecial() {
			continue
		}
		count++
		for _, c := range chapterLine.FindAllStringSubmatch(block, -1) {
			chapter := md.NewIdentifier(c[1])
			if _, ok := result[chapter]; !ok {
				result[chapter] = volume
			}
		}
	}

	return result, count
}
