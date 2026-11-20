package job

import (
	"fmt"
	"sort"

	"github.com/leotaku/kojirou/cmd/adstrip"
	md "github.com/leotaku/kojirou/mangadex"
)

// removeAds cuts the advertising strips a website adds to the first and last
// page of each chapter. It learns the strips from the chapters being
// downloaded (a strip is the same picture on several chapters) and also uses
// the ones already known for the source. It returns the pages and how many
// were cut.
func removeAds(images md.ImageList, known []adstrip.Strip, learned func([]adstrip.Strip)) (md.ImageList, int) {
	// The first and last page of every chapter are the only ones that carry ads.
	byChapter := map[md.Identifier][]int{}
	var order []md.Identifier
	for i, img := range images {
		if _, seen := byChapter[img.ChapterIdentifier]; !seen {
			order = append(order, img.ChapterIdentifier)
		}
		byChapter[img.ChapterIdentifier] = append(byChapter[img.ChapterIdentifier], i)
	}
	var edge []int
	for _, chapter := range order {
		idx := byChapter[chapter]
		sort.Slice(idx, func(a, b int) bool { return images[idx[a]].ImageIdentifier < images[idx[b]].ImageIdentifier })
		edge = append(edge, idx[0])
		if len(idx) > 1 {
			edge = append(edge, idx[len(idx)-1])
		}
	}

	pages := make([]*adstrip.Page, len(edge))
	for i, at := range edge {
		pages[i] = adstrip.Analyze(images[at].Image)
	}
	strips := known
	if found := adstrip.Detect(pages); len(found) > 0 {
		merged := adstrip.Merge(known, found)
		if len(merged) > len(known) && learned != nil {
			learned(merged[len(known):])
		}
		strips = merged
	}
	if len(strips) == 0 {
		return images, 0
	}

	out := append(md.ImageList(nil), images...)
	cropped := 0
	for i, at := range edge {
		cut := adstrip.Find(pages[i], strips)
		if cut.Empty() {
			continue
		}
		out[at].Image = adstrip.Crop(images[at].Image, cut)
		cropped++
	}

	return out, cropped
}

func adsStepTitle(n int) string {
	return fmt.Sprintf("Advertising strips cut from %d pages", n)
}
