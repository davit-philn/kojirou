package api

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
)

// SearchItem is a manga as returned by the search and detail endpoints with
// cover art and authors included.
type SearchItem struct {
	ID         string
	Attributes struct {
		Title                        Localized
		AltTitles                    []Localized
		Description                  Localized
		Status                       string
		Year                         int
		OriginalLanguage             string
		AvailableTranslatedLanguages []string
		ContentRating                string
	}
	Relationships []IncludedRelationship
}

// IncludedRelationship keeps the attributes of expanded relationships.
type IncludedRelationship struct {
	ID         string
	Type       string
	Attributes json.RawMessage
}

// CoverFileName returns the file name of the cover art, if included.
func (s SearchItem) CoverFileName() string {
	for _, r := range s.Relationships {
		if r.Type == "cover_art" {
			var a struct{ FileName string }
			if json.Unmarshal(r.Attributes, &a) == nil {
				return a.FileName
			}
		}
	}

	return ""
}

// AuthorNames returns the names of included authors.
func (s SearchItem) AuthorNames() []string {
	names := make([]string, 0)
	for _, r := range s.Relationships {
		if r.Type == "author" {
			var a struct{ Name string }
			if json.Unmarshal(r.Attributes, &a) == nil && a.Name != "" {
				names = append(names, a.Name)
			}
		}
	}

	return names
}

type SearchResult struct {
	Result string
	Data   []SearchItem
	Limit  int
	Offset int
	Total  int
}

type SearchItemResponse struct {
	Result string
	Data   SearchItem
}

type SearchQuery struct {
	Title            string
	Language         string
	Order            string // "relevance", "followedCount", "latestUploadedChapter", "createdAt"
	IncludeAdult     bool
	Limit            int
	Offset           int
	AvailableChapter bool
}

func (q SearchQuery) values() url.Values {
	v := make(url.Values)
	if q.Title != "" {
		v.Set("title", q.Title)
	}
	if q.Language != "" {
		v.Add("availableTranslatedLanguage[]", q.Language)
	}
	if q.Order != "" {
		v.Set("order["+q.Order+"]", "desc")
	}
	for _, rating := range contentRatings(q.IncludeAdult) {
		v.Add("contentRating[]", rating)
	}
	if q.AvailableChapter {
		v.Set("hasAvailableChapters", "true")
	}
	v.Add("includes[]", "cover_art")
	v.Add("includes[]", "author")
	if q.Limit > 0 {
		v.Set("limit", strconv.Itoa(q.Limit))
	}
	if q.Offset > 0 {
		v.Set("offset", strconv.Itoa(q.Offset))
	}

	return v
}

func contentRatings(adult bool) []string {
	if adult {
		return []string{"safe", "suggestive", "erotica", "pornographic"}
	}

	return []string{"safe", "suggestive"}
}

func (c *Client) SearchManga(ctx context.Context, q SearchQuery) (*SearchResult, error) {
	v := new(SearchResult)
	err := c.doJSON(ctx, "GET", "/manga?"+q.values().Encode(), v, nil)
	return v, err
}

func (c *Client) GetMangaDetail(ctx context.Context, mangaID string) (*SearchItemResponse, error) {
	v := new(SearchItemResponse)
	query := url.Values{"includes[]": {"cover_art", "author"}}
	err := c.doJSON(ctx, "GET", "/manga/"+url.PathEscape(mangaID)+"?"+query.Encode(), v, nil)
	return v, err
}
