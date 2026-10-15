package mangadex

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
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

	// Translations of a chapter can disagree about its volume, so keep every
	// candidate and settle the conflicts afterwards.
	candidates := make(map[Identifier][]Identifier)
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
			if !contains(candidates[id], volume) {
				candidates[id] = append(candidates[id], volume)
			}
		}
	}

	return consistentVolumes(candidates), nil
}

func contains(list []Identifier, id Identifier) bool {
	for _, x := range list {
		if x.Equal(id) {
			return true
		}
	}

	return false
}

// maxVolumePairs bounds the quadratic search in consistentVolumes.
const maxVolumePairs = 20000

// consistentVolumes picks one volume per chapter such that volumes never go
// down as chapter numbers go up: it keeps the largest set of chapters for
// which that holds, preferring the lowest candidate. A stray tag, such as
// one group calling chapter 150 "volume 1", is then dropped instead of
// winning by accident; dropped chapters count as unknown.
func consistentVolumes(candidates map[Identifier][]Identifier) map[Identifier]Identifier {
	chapters := make([]Identifier, 0, len(candidates))
	for ch := range candidates {
		chapters = append(chapters, ch)
	}
	sort.Slice(chapters, func(i, j int) bool { return chapters[i].Less(chapters[j]) })

	type pair struct {
		chapter int
		volume  Identifier
	}
	pairs := make([]pair, 0, len(chapters))
	for i, ch := range chapters {
		vols := append([]Identifier(nil), candidates[ch]...)
		sort.Slice(vols, func(a, b int) bool { return vols[a].Less(vols[b]) })
		for _, v := range vols {
			pairs = append(pairs, pair{i, v})
		}
	}

	result := make(map[Identifier]Identifier, len(chapters))
	if len(pairs) > maxVolumePairs {
		for _, p := range pairs {
			if _, ok := result[chapters[p.chapter]]; !ok {
				result[chapters[p.chapter]] = p.volume
			}
		}

		return result
	}

	best := make([]int, len(pairs))
	prev := make([]int, len(pairs))
	top := -1
	for i := range pairs {
		best[i], prev[i] = 1, -1
		for j := 0; j < i; j++ {
			if pairs[j].chapter < pairs[i].chapter && pairs[j].volume.LessOrEqual(pairs[i].volume) && best[j]+1 > best[i] {
				best[i], prev[i] = best[j]+1, j
			}
		}
		if top < 0 || best[i] > best[top] {
			top = i
		}
	}
	for i := top; i >= 0; i = prev[i] {
		result[chapters[pairs[i].chapter]] = pairs[i].volume
	}

	return result
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
