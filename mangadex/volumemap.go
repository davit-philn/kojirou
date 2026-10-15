package mangadex

import (
	"context"
	"encoding/json"
	"fmt"
)

// FetchVolumeMap returns which volume each chapter number belongs to, over
// all translations. Chapters without a volume are left out. It uses the
// aggregate endpoint, which also knows chapters that are hosted elsewhere and
// translations without a volume, so it is far more complete than the feed.
func (c *Client) FetchVolumeMap(ctx context.Context, mangaID string) (map[Identifier]Identifier, error) {
	raw, err := c.base.GetAggregate(ctx, mangaID)
	if err != nil {
		return nil, fmt.Errorf("get volumes: %w", err)
	}

	return parseAggregate(raw)
}

type aggregateVolume struct {
	Volume   string          `json:"volume"`
	Chapters json.RawMessage `json:"chapters"`
}

type aggregateChapter struct {
	Chapter string `json:"chapter"`
}

// parseAggregate reads the "volumes" value of an aggregate response. The API
// returns JSON objects keyed by volume and chapter, but turns them into
// arrays when the keys happen to be 0, 1, 2…, so both shapes are accepted.
func parseAggregate(raw json.RawMessage) (map[Identifier]Identifier, error) {
	var volumes []aggregateVolume
	if err := decodeObjectOrArray(raw, &volumes); err != nil {
		return nil, fmt.Errorf("decode volumes: %w", err)
	}

	result := make(map[Identifier]Identifier)
	for _, v := range volumes {
		if v.Volume == "" || v.Volume == "none" {
			continue
		}
		volume := NewIdentifier(v.Volume)

		var chapters []aggregateChapter
		if err := decodeObjectOrArray(v.Chapters, &chapters); err != nil {
			return nil, fmt.Errorf("decode chapters of volume %v: %w", v.Volume, err)
		}
		for _, ch := range chapters {
			id := NewIdentifier(ch.Chapter)
			if ch.Chapter == "" || ch.Chapter == "none" || id.IsSpecial() {
				continue
			}
			if _, ok := result[id]; !ok {
				result[id] = volume
			}
		}
	}

	return result, nil
}

// decodeObjectOrArray fills out (a pointer to a slice) from either a JSON
// array or the values of a JSON object. Empty or null input gives no items.
func decodeObjectOrArray[T any](raw json.RawMessage, out *[]T) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	if raw[0] == '[' {
		return json.Unmarshal(raw, out)
	}

	var object map[string]T
	if err := json.Unmarshal(raw, &object); err != nil {
		return err
	}
	for _, v := range object {
		*out = append(*out, v)
	}

	return nil
}
