package mangadex

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/leotaku/kojirou/mangadex/api"
)

// SeriesSummary is what a listing or detail page needs to show a series.
type SeriesSummary struct {
	ID          string
	Title       string
	Description string
	Status      string
	Year        int
	Authors     []string
	Languages   []string
	CoverURL    string
}

type SearchParams struct {
	Title        string
	Language     string
	Order        string
	IncludeAdult bool
	Limit        int
	Offset       int
}

// Search lists series. Without a title, Order decides what is listed
// (e.g. "followedCount" for popular, "latestUploadedChapter" for latest).
func (c *Client) Search(ctx context.Context, p SearchParams) ([]SeriesSummary, int, error) {
	order := p.Order
	if order == "" {
		order = "relevance"
		if p.Title == "" {
			order = "followedCount"
		}
	}
	limit := p.Limit
	if limit <= 0 || limit > 100 {
		limit = 24
	}

	res, err := c.base.SearchManga(ctx, api.SearchQuery{
		Title:            p.Title,
		Language:         p.Language,
		Order:            order,
		IncludeAdult:     p.IncludeAdult,
		Limit:            limit,
		Offset:           p.Offset,
		AvailableChapter: true,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("search: %w", err)
	}

	list := make([]SeriesSummary, 0, len(res.Data))
	for _, item := range res.Data {
		list = append(list, c.summarize(item))
	}

	return list, res.Total, nil
}

// Describe returns the summary of one series.
func (c *Client) Describe(ctx context.Context, mangaID string) (*SeriesSummary, error) {
	res, err := c.base.GetMangaDetail(ctx, mangaID)
	if err != nil {
		return nil, fmt.Errorf("describe: %w", err)
	}
	s := c.summarize(res.Data)

	return &s, nil
}

func (c *Client) summarize(item api.SearchItem) SeriesSummary {
	langs := append([]string(nil), item.Attributes.AvailableTranslatedLanguages...)
	sort.Strings(langs)

	s := SeriesSummary{
		ID:          item.ID,
		Title:       localized(item.Attributes.Title),
		Description: localized(item.Attributes.Description),
		Status:      item.Attributes.Status,
		Year:        item.Attributes.Year,
		Authors:     item.AuthorNames(),
		Languages:   langs,
	}
	if file := item.CoverFileName(); file != "" {
		// 256px thumbnails keep listings light.
		s.CoverURL = strings.TrimRight(c.coverBaseURL.String(), "/") + "/" +
			url.PathEscape(item.ID) + "/" + url.PathEscape(file) + ".256.jpg"
	}

	return s
}

// WithBaseURLs points the client at different API and cover servers,
// e.g. for tests.
func (c *Client) WithBaseURLs(api, covers url.URL) *Client {
	c.base.WithBaseURL(api)
	c.coverBaseURL = covers
	return c
}

// localized picks a stable value from a localized map: English if present,
// otherwise the first language alphabetically. Empty maps give "".
func localized(m map[string]string) string {
	if v, ok := m["en"]; ok {
		return v
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return ""
	}

	return m[keys[0]]
}
