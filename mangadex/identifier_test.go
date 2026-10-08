package mangadex

import (
	"encoding/json"
	"testing"
)

func TestIdentifierString(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"1", "1"},
		{"10", "10"},
		{"1.5", "1.5"},
		{"1.0", "1"},
		{"Special", "Special"},
	}
	for _, tt := range tests {
		if got := NewIdentifier(tt.in).String(); got != tt.want {
			t.Errorf("NewIdentifier(%q).String() = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestIdentifierStringFilled(t *testing.T) {
	if got := NewIdentifier("2").StringFilled(2, 0, false); got != "02" {
		t.Errorf("got %q, want %q", got, "02")
	}
	if got := NewIdentifier("2.5").StringFilled(2, 1, false); got != "02.5" {
		t.Errorf("got %q, want %q", got, "02.5")
	}
	if got := NewIdentifier("2").StringFilled(2, 1, true); got != "02.0" {
		t.Errorf("got %q, want %q", got, "02.0")
	}
}

func TestIdentifierUnknown(t *testing.T) {
	if !UnknownIdentifier().IsUnknown() {
		t.Error("UnknownIdentifier should be unknown")
	}
	if got := NewWithFallback("", "Unknown").String(); got != "Unknown" {
		t.Errorf("got %q, want Unknown", got)
	}
	if NewIdentifier("Special").IsUnknown() {
		t.Error("named special identifier must not be unknown")
	}
}

func TestIdentifierOrdering(t *testing.T) {
	ordered := []Identifier{
		NewIdentifier("1"),
		NewIdentifier("1.5"),
		NewIdentifier("2"),
		NewIdentifier("10"), // numeric, not lexicographic
		NewIdentifier("Extra"),
		NewIdentifier("Special"),
	}
	for i := range ordered {
		for j := range ordered {
			if got, want := ordered[i].Less(ordered[j]), i < j; got != want {
				t.Errorf("%v.Less(%v) = %v, want %v", ordered[i], ordered[j], got, want)
			}
			if got, want := ordered[i].Equal(ordered[j]), i == j; got != want {
				t.Errorf("%v.Equal(%v) = %v, want %v", ordered[i], ordered[j], got, want)
			}
		}
	}
}

func TestIdentifierIsNext(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"1", "2", true},
		{"1", "1.5", true},
		{"1.5", "2", true},
		{"1", "3", false},
		{"2", "1", false},
	}
	for _, tt := range tests {
		if got := NewIdentifier(tt.a).IsNext(NewIdentifier(tt.b)); got != tt.want {
			t.Errorf("%v.IsNext(%v) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestIdentifierJSONRoundTrip(t *testing.T) {
	for _, s := range []string{"3", "3.5", "Special"} {
		in := NewIdentifier(s)
		data, err := json.Marshal(in)
		if err != nil {
			t.Fatal(err)
		}
		var out Identifier
		if err := json.Unmarshal(data, &out); err != nil {
			t.Fatal(err)
		}
		if !in.Equal(out) {
			t.Errorf("round trip of %q gave %v", s, out)
		}
	}
}
