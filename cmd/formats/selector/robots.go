package selector

import (
	"bufio"
	"regexp"
	"strings"
)

// robotsRules holds the Allow/Disallow rules of a robots.txt that apply to
// this tool (its own group if present, else the "*" group).
type robotsRules struct {
	rules []robotsRule
}

type robotsRule struct {
	allow   bool
	pattern *regexp.Regexp
	length  int
}

// parseRobots reads a robots.txt for the given user agent name.
func parseRobots(text, agent string) *robotsRules {
	agent = strings.ToLower(agent)
	type group struct {
		agents []string
		rules  []robotsRule
	}
	var groups []*group
	var current *group
	lastWasAgent := false

	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		line := sc.Text()
		if i := strings.Index(line, "#"); i >= 0 {
			line = line[:i]
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key, value = strings.ToLower(strings.TrimSpace(key)), strings.TrimSpace(value)
		switch key {
		case "user-agent":
			if current == nil || !lastWasAgent {
				current = &group{}
				groups = append(groups, current)
			}
			current.agents = append(current.agents, strings.ToLower(value))
			lastWasAgent = true
		case "allow", "disallow":
			lastWasAgent = false
			if current == nil || value == "" {
				continue // an empty Disallow allows everything
			}
			re, err := regexp.Compile(robotsPattern(value))
			if err != nil {
				continue
			}
			current.rules = append(current.rules, robotsRule{allow: key == "allow", pattern: re, length: len(value)})
		default:
			lastWasAgent = false
		}
	}

	var own, star *group
	for _, g := range groups {
		for _, a := range g.agents {
			switch {
			case a != "*" && strings.Contains(agent, a):
				own = g
			case a == "*":
				star = g
			}
		}
	}
	switch {
	case own != nil:
		return &robotsRules{rules: own.rules}
	case star != nil:
		return &robotsRules{rules: star.rules}
	default:
		return &robotsRules{}
	}
}

// robotsPattern turns a robots.txt path pattern (with * and $) into a regexp
// anchored at the start of the path.
func robotsPattern(p string) string {
	var b strings.Builder
	b.WriteString("^")
	for _, r := range p {
		switch r {
		case '*':
			b.WriteString(".*")
		case '$':
			b.WriteString("$")
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}

	return b.String()
}

// allowed reports whether the path (with query) may be fetched. The longest
// matching rule wins and Allow wins a tie, as in the usual convention.
func (r *robotsRules) allowed(path string) bool {
	if r == nil {
		return true
	}
	best := -1
	allow := true
	for _, rule := range r.rules {
		if !rule.pattern.MatchString(path) {
			continue
		}
		if rule.length > best || (rule.length == best && rule.allow) {
			best = rule.length
			allow = rule.allow
		}
	}

	return allow
}
