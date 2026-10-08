package filter

import (
	"testing"

	md "github.com/leotaku/kojirou/mangadex"
)

func TestRangesContains(t *testing.T) {
	tests := []struct {
		expr string
		id   string
		want bool
	}{
		{"3", "3", true},
		{"3", "4", false},
		{"1..5", "1", true},
		{"1..5", "5", true},
		{"1..5", "3.5", true},
		{"1..5", "6", false},
		{"1..3,7", "7", true},
		{"1..3,7", "5", false},
		{"!1..3", "2", false},
		{"!1..3", "4", true},
		{"!5", "5", false},
		{"!5", "6", true},
	}
	for _, tt := range tests {
		rs := ParseRanges(tt.expr)
		if got := rs.Contains(md.NewIdentifier(tt.id)); got != tt.want {
			t.Errorf("ParseRanges(%q).Contains(%q) = %v, want %v", tt.expr, tt.id, got, tt.want)
		}
	}
}
