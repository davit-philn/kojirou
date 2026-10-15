package selector

import (
	"fmt"
	"sort"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/andybalholm/cascadia"
)

// challengePhrases appear on browser-check pages that sit in front of some
// websites. This tool only reads plain HTML and cannot pass such checks.
var challengePhrases = []string{
	"just a moment",
	"attention required",
	"checking your browser",
	"enable javascript and cookies",
	"verify you are human",
	"access denied",
	"đang kiểm tra trình duyệt",
}

// describeNoMatch explains why a selector found nothing usable on a page, so
// that the user can fix the configuration without guessing. attrs is only set
// when looking for image URLs.
func describeNoMatch(doc *goquery.Document, sel cascadia.Selector, selector string, attrs []string, thing string) string {
	title := strings.TrimSpace(doc.Find("title").First().Text())
	if len(title) > 80 {
		title = title[:80] + "…"
	}

	matched := doc.FindMatcher(sel)
	if matched.Length() > 0 && len(attrs) > 0 {
		return fmt.Sprintf(
			"%d elements matched %q but none of them has a usable %v (an http(s) address). The first element has these attributes: %v. "+
				"Set image_attr to the one that holds the real image address, or list several separated by commas, e.g. \"data-original,data-src,src\"",
			matched.Length(), selector, quoteAll(attrs), attributeNames(matched.First()))
	}

	msg := fmt.Sprintf("nothing matched %q (page title: %q, %d <a> and %d <img> elements on the page)",
		selector, title, doc.Find("a").Length(), doc.Find("img").Length())
	if looksLikeChallenge(doc, title) {
		return msg + ". The page looks like a browser check (anti-bot) page. This tool only reads plain HTML and cannot pass it"
	}

	return msg + fmt.Sprintf(". The selector for the %v may be wrong, or the page may add its content with JavaScript, which this tool does not run. "+
		"Compare the selector with \"View page source\" (Ctrl+U) in your browser, not with the Elements tab", thing)
}

func attributeNames(s *goquery.Selection) []string {
	names := make([]string, 0)
	if len(s.Nodes) == 0 {
		return names
	}
	for _, a := range s.Nodes[0].Attr {
		names = append(names, a.Key)
	}
	sort.Strings(names)

	return names
}

func quoteAll(items []string) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = fmt.Sprintf("%q", it)
	}

	return out
}

func looksLikeChallenge(doc *goquery.Document, title string) bool {
	text := strings.ToLower(title + " " + doc.Find("body").Text())
	if len(text) > 4000 {
		text = text[:4000]
	}
	for _, phrase := range challengePhrases {
		if strings.Contains(text, phrase) {
			return true
		}
	}

	return false
}

// splitAttrs turns "data-original, data-src,src" into the list of attribute
// names to try in order.
func splitAttrs(spec string) []string {
	attrs := make([]string, 0)
	for _, a := range strings.Split(spec, ",") {
		if a = strings.TrimSpace(a); a != "" {
			attrs = append(attrs, a)
		}
	}
	if len(attrs) == 0 {
		return []string{defaultImageAttr}
	}

	return attrs
}
