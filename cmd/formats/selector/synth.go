package selector

import (
	"regexp"
	"sort"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/andybalholm/cascadia"
	"golang.org/x/net/html"
)

var (
	stableClass   = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,29}$`)
	unstableClass = regexp.MustCompile(`(?i)(^|[-_])(active|current|selected|first|last|odd|even|hidden|show|open|disabled|hover|focus|clearfix)($|[-_])|\d{3,}`)
)

// stableClasses returns the class names of a node that look like they describe
// its role rather than its state or position.
func stableClasses(n *html.Node) []string {
	var out []string
	for _, a := range n.Attr {
		if a.Key != "class" {
			continue
		}
		for _, c := range strings.Fields(a.Val) {
			if stableClass.MatchString(c) && !unstableClass.MatchString(c) {
				out = append(out, c)
			}
		}
	}
	sort.Strings(out)

	return out
}

func classSet(n *html.Node) map[string]bool {
	set := map[string]bool{}
	for _, c := range stableClasses(n) {
		set[c] = true
	}

	return set
}

// nodeSignature describes an element by tag and stable classes, e.g. "li.item".
func nodeSignature(n *html.Node) string {
	if n == nil || n.Type != html.ElementNode {
		return ""
	}

	return n.Data + "." + strings.Join(stableClasses(n), ".")
}

// parentSignature combines the signatures of the two nearest ancestors; items
// of the same list share it.
func parentSignature(n *html.Node) string {
	p := n.Parent
	if p == nil {
		return ""
	}

	return nodeSignature(p) + "<" + nodeSignature(p.Parent)
}

// synthesize builds a short CSS selector that matches exactly the given
// elements. It tries the element alone, then adds ancestors one by one and
// keeps the shortest selector whose matches equal the targets. If none is
// exact it returns the closest one that covers all targets with few extras.
func synthesize(doc *goquery.Document, targets []*html.Node) (string, bool) {
	if len(targets) == 0 {
		return "", false
	}
	want := make(map[*html.Node]bool, len(targets))
	for _, n := range targets {
		want[n] = true
	}

	best, bestScore := "", 0.0
	for depth := 0; depth <= 4; depth++ {
		candidate, ok := candidateSelector(targets, depth)
		if !ok {
			break
		}
		sel, err := cascadia.Compile(candidate)
		if err != nil {
			continue
		}
		got := doc.FindMatcher(sel).Nodes
		hit := 0
		for _, n := range got {
			if want[n] {
				hit++
			}
		}
		if hit == len(targets) && len(got) == len(targets) {
			return candidate, true
		}
		if hit == len(targets) {
			if score := float64(len(targets)) / float64(len(got)); score > bestScore {
				best, bestScore = candidate, score
			}
		}
	}

	return best, bestScore >= 0.8
}

// candidateSelector builds the selector made of the common tag/classes of the
// targets and of their `depth` nearest ancestors.
func candidateSelector(targets []*html.Node, depth int) (string, bool) {
	level := append([]*html.Node(nil), targets...)
	parts := make([]string, 0, depth+1)
	for d := 0; d <= depth; d++ {
		part, ok := commonPart(level)
		if !ok {
			return "", false
		}
		parts = append([]string{part}, parts...)
		for i, n := range level {
			level[i] = n.Parent
		}
	}

	return strings.Join(parts, " "), true
}

// commonPart returns tag plus the classes shared by all nodes, or false if
// the nodes are not the same kind of element.
func commonPart(nodes []*html.Node) (string, bool) {
	var common map[string]bool
	tag := ""
	for i, n := range nodes {
		if n == nil || n.Type != html.ElementNode {
			return "", false
		}
		if i == 0 {
			tag = n.Data
			common = classSet(n)
			continue
		}
		if n.Data != tag {
			return "", false
		}
		for c := range common {
			if !classSet(n)[c] {
				delete(common, c)
			}
		}
	}

	names := make([]string, 0, len(common))
	for c := range common {
		names = append(names, c)
	}
	sort.Strings(names)
	part := tag
	for _, c := range names {
		part += "." + c
	}

	return part, true
}
