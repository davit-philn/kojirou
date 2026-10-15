package selector

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// volumeLabel limits volume names to something safe to use in file names.
var volumeLabel = regexp.MustCompile(`^[A-Za-z0-9]{1,20}(\.[0-9]{1,3})?$`)

type volumeRange struct {
	volume   string
	from, to float64
}

// volumeRules decides which volume a chapter belongs to. Rules are tried in
// this order: ranges, then the regular expression.
type volumeRules struct {
	ranges []volumeRange
	re     *regexp.Regexp
}

func (r *volumeRules) configured() bool {
	return r != nil && (len(r.ranges) > 0 || r.re != nil)
}

// compileVolumeRules validates and prepares the volume rules of a config.
func compileVolumeRules(cfg SourceConfig) (*volumeRules, error) {
	rules := &volumeRules{}
	if strings.TrimSpace(cfg.VolumeRanges) != "" {
		ranges, err := parseVolumeRanges(cfg.VolumeRanges)
		if err != nil {
			return nil, fmt.Errorf("invalid volume_ranges: %w", err)
		}
		rules.ranges = ranges
	}
	if strings.TrimSpace(cfg.VolumeRegex) != "" {
		re, err := regexp.Compile(cfg.VolumeRegex)
		if err != nil {
			return nil, fmt.Errorf("invalid volume_regex: %w", err)
		}
		if re.NumSubexp() < 1 {
			return nil, fmt.Errorf("invalid volume_regex: it needs a capture group around the volume, e.g. Tập\\s*(\\d+)")
		}
		rules.re = re
	}

	return rules, nil
}

// parseVolumeRanges reads specs such as "1:1-8, 2:9-17; Special:100" where
// each item is volume:firstChapter-lastChapter (or volume:chapter).
func parseVolumeRanges(spec string) ([]volumeRange, error) {
	items := strings.FieldsFunc(spec, func(r rune) bool { return r == ',' || r == ';' || r == '\n' })
	ranges := make([]volumeRange, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		volume, span, ok := strings.Cut(item, ":")
		volume, span = strings.TrimSpace(volume), strings.TrimSpace(span)
		if !ok || volume == "" || span == "" {
			return nil, fmt.Errorf("%q: use volume:first-last, e.g. 1:1-8", item)
		}
		if !volumeLabel.MatchString(volume) {
			return nil, fmt.Errorf("%q: the volume name may only contain letters and digits", volume)
		}

		sep := "-"
		if strings.Contains(span, "..") {
			sep = ".."
		}
		first, last, isRange := strings.Cut(span, sep)
		if !isRange {
			last = first
		}
		from, err1 := strconv.ParseFloat(strings.TrimSpace(first), 64)
		to, err2 := strconv.ParseFloat(strings.TrimSpace(last), 64)
		if err1 != nil || err2 != nil || from < 0 || to < 0 {
			return nil, fmt.Errorf("%q: chapter numbers must be plain numbers, e.g. 1-8 or 3.5", item)
		}
		if from > to {
			return nil, fmt.Errorf("%q: the first chapter is after the last one", item)
		}
		ranges = append(ranges, volumeRange{volume: volume, from: from, to: to})
	}
	if len(ranges) == 0 {
		return nil, fmt.Errorf("no ranges found")
	}

	return ranges, nil
}

// volumeFor returns the volume for a chapter, or false if no rule applies.
func (r *volumeRules) volumeFor(title, link string, number float64, hasNumber bool) (string, bool) {
	if r == nil {
		return "", false
	}
	if hasNumber {
		for _, rg := range r.ranges {
			if number >= rg.from && number <= rg.to {
				return rg.volume, true
			}
		}
	}
	if r.re != nil {
		for _, text := range []string{title, link} {
			if m := r.re.FindStringSubmatch(text); m != nil {
				if label := strings.TrimSpace(m[1]); volumeLabel.MatchString(label) {
					return label, true
				}
			}
		}
	}

	return "", false
}
