// Package multi puts the answers of several sources together: it recognises
// the same series under different names and merges the chapter lists into one
// table that shows which chapters are missing.
package multi

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// Candidate is one series a source found for a search.
type Candidate struct {
	Source string `json:"source"`
	Title  string `json:"title"`
	// ID is the series ID on MangaDex, or the series page's address.
	ID    string `json:"id"`
	Cover string `json:"cover,omitempty"`
}

// Group is the same series as found by several sources.
type Group struct {
	Title   string      `json:"title"`
	Members []Candidate `json:"members"`
	// Sources is how many different sources found it.
	Sources int `json:"sources"`
}

var bracketed = regexp.MustCompile(`[\(\[【（][^\)\]】）]*[\)\]】）]`)

// Normalize reduces a title to lower case words without accents or
// punctuation, so that "Blue Lock", "BLUE-LOCK" and "Blue Lock (Tiếng Việt)"
// are the same.
func Normalize(title string) string {
	title = bracketed.ReplaceAllString(title, " ")
	title = strings.NewReplacer("đ", "d", "Đ", "d").Replace(title)
	stripped, _, err := transform.String(transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC), title)
	if err != nil {
		stripped = title
	}
	var b strings.Builder
	space := true
	for _, r := range strings.ToLower(stripped) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			space = false
		} else if !space {
			b.WriteRune(' ')
			space = true
		}
	}

	return strings.TrimSpace(b.String())
}

// sameSeries tells whether two normalised titles name the same series:
// equal, or the same words in another order.
func sameSeries(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	ta, tb := strings.Fields(a), strings.Fields(b)
	if len(ta) != len(tb) || len(ta) < 2 {
		return false
	}
	sort.Strings(ta)
	sort.Strings(tb)
	for i := range ta {
		if ta[i] != tb[i] {
			return false
		}
	}

	return true
}

// GroupCandidates gathers the candidates that are the same series. The groups
// found by most sources come first. Two candidates of one source may share a
// group (a source can list a series twice); the person unticks the wrong one.
func GroupCandidates(cands []Candidate) []Group {
	type bucket struct {
		key   string
		group Group
	}
	var buckets []*bucket
	for _, c := range cands {
		key := Normalize(c.Title)
		var found *bucket
		for _, b := range buckets {
			if sameSeries(b.key, key) {
				found = b
				break
			}
		}
		if found == nil {
			found = &bucket{key: key, group: Group{Title: c.Title}}
			buckets = append(buckets, found)
		}
		found.group.Members = append(found.group.Members, c)
	}
	groups := make([]Group, 0, len(buckets))
	for _, b := range buckets {
		seen := map[string]bool{}
		for _, m := range b.group.Members {
			seen[m.Source] = true
		}
		b.group.Sources = len(seen)
		// MangaDex's title is the cleanest, else the first one found.
		for _, m := range b.group.Members {
			if m.Source == "mangadex" {
				b.group.Title = m.Title
				break
			}
		}
		groups = append(groups, b.group)
	}
	sort.SliceStable(groups, func(i, j int) bool { return groups[i].Sources > groups[j].Sources })

	return groups
}

// Chapter is one chapter as a source lists it.
type Chapter struct {
	Number string   `json:"number"`
	Title  string   `json:"title,omitempty"`
	ID     string   `json:"id"`
	Groups []string `json:"groups,omitempty"`
}

// Entry says that a source has a chapter.
type Entry struct {
	Source string   `json:"source"`
	ID     string   `json:"id"`
	Title  string   `json:"title,omitempty"`
	Groups []string `json:"groups,omitempty"`
}

// Row is one chapter number and every source that has it.
type Row struct {
	Number  string  `json:"number"`
	Value   float64 `json:"value"`
	Sources []Entry `json:"sources"`
}

// SourceStat summarises what one source has.
type SourceStat struct {
	Source string `json:"source"`
	Count  int    `json:"count"`
	First  string `json:"first,omitempty"`
	Last   string `json:"last,omitempty"`
	// Missing lists the whole chapter numbers this source lacks between
	// chapter 1 and its last chapter, as ranges ("3", "10-12").
	Missing []string `json:"missing"`
}

// Table is the merged chapter list.
type Table struct {
	Rows []Row `json:"rows"`
	// Others are chapters whose number is not a number (extras, specials).
	Others []Row `json:"others"`
	// Gaps are the whole chapter numbers no source has, as ranges.
	Gaps  []string     `json:"gaps"`
	Stats []SourceStat `json:"stats"`
	// First and Last bound the numbered chapters.
	First string `json:"first,omitempty"`
	Last  string `json:"last,omitempty"`
}

// FormatNumber prints a chapter number without a useless ".0".
func FormatNumber(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// Merge puts the chapter lists of several sources into one table.
func Merge(lists map[string][]Chapter) Table {
	numbered := map[float64]*Row{}
	others := map[string]*Row{}
	stats := make([]SourceStat, 0, len(lists))

	names := make([]string, 0, len(lists))
	for name := range lists {
		names = append(names, name)
	}
	sort.Strings(names)

	have := map[string]map[int]bool{} // source -> whole chapter numbers it has
	for _, name := range names {
		have[name] = map[int]bool{}
		st := SourceStat{Source: name, Missing: []string{}}
		maxV, minV := math.Inf(-1), math.Inf(1)
		for _, c := range lists[name] {
			entry := Entry{Source: name, ID: c.ID, Title: c.Title, Groups: c.Groups}
			v, err := strconv.ParseFloat(strings.TrimSpace(c.Number), 64)
			if err != nil || v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
				key := strings.TrimSpace(c.Number)
				if key == "" {
					key = c.Title
				}
				if others[key] == nil {
					others[key] = &Row{Number: key}
				}
				others[key].Sources = append(others[key].Sources, entry)
				continue
			}
			st.Count++
			if numbered[v] == nil {
				numbered[v] = &Row{Number: FormatNumber(v), Value: v}
			}
			numbered[v].Sources = append(numbered[v].Sources, entry)
			if v == math.Trunc(v) {
				have[name][int(v)] = true
			}
			maxV, minV = math.Max(maxV, v), math.Min(minV, v)
		}
		if st.Count > 0 {
			st.First, st.Last = FormatNumber(minV), FormatNumber(maxV)
			st.Missing = ranges(missingWhole(have[name], minV, maxV))
		}
		stats = append(stats, st)
	}

	t := Table{Rows: []Row{}, Others: []Row{}, Gaps: []string{}, Stats: stats}
	for _, r := range numbered {
		t.Rows = append(t.Rows, *r)
	}
	sort.Slice(t.Rows, func(i, j int) bool { return t.Rows[i].Value < t.Rows[j].Value })
	for _, r := range others {
		t.Others = append(t.Others, *r)
	}
	sort.Slice(t.Others, func(i, j int) bool { return t.Others[i].Number < t.Others[j].Number })

	if len(t.Rows) > 0 {
		first, last := t.Rows[0].Value, t.Rows[len(t.Rows)-1].Value
		t.First, t.Last = FormatNumber(first), FormatNumber(last)
		all := map[int]bool{}
		for _, r := range t.Rows {
			if r.Value == math.Trunc(r.Value) {
				all[int(r.Value)] = true
			}
		}
		t.Gaps = ranges(missingWhole(all, first, last))
	}

	return t
}

// missingWhole lists the whole numbers from 1 (or 0, if the list starts
// there) to the last chapter that are not in have.
func missingWhole(have map[int]bool, first, last float64) []int {
	start := 1
	if first < 1 {
		start = 0
	}
	var out []int
	for n := start; float64(n) <= last; n++ {
		if !have[n] {
			out = append(out, n)
		}
	}

	return out
}

// ranges turns 3,4,5,9 into "3-5", "9".
func ranges(nums []int) []string {
	out := []string{}
	for i := 0; i < len(nums); {
		j := i
		for j+1 < len(nums) && nums[j+1] == nums[j]+1 {
			j++
		}
		if j == i {
			out = append(out, strconv.Itoa(nums[i]))
		} else {
			out = append(out, fmt.Sprintf("%d-%d", nums[i], nums[j]))
		}
		i = j + 1
	}

	return out
}

// Rank puts the groups whose title has every word of the search first, and
// within those (and within the rest) the ones found by more sources.
func Rank(groups []Group, query string) {
	words := strings.Fields(Normalize(query))
	matches := func(g Group) bool {
		title := " " + Normalize(g.Title) + " "
		for _, w := range words {
			if !strings.Contains(title, " "+w+" ") {
				return false
			}
		}

		return len(words) > 0
	}
	sort.SliceStable(groups, func(i, j int) bool {
		mi, mj := matches(groups[i]), matches(groups[j])
		if mi != mj {
			return mi
		}

		return groups[i].Sources > groups[j].Sources
	})
}
