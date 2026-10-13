package mangadex

import "testing"

func TestParseSeriesID(t *testing.T) {
	const id = "c727d921-4294-455b-9bcc-2f0c7d29cd9a"
	tests := []struct {
		in   string
		want string
		ok   bool
	}{
		{id, id, true},
		{"  " + id + "\n", id, true},
		{"C727D921-4294-455B-9BCC-2F0C7D29CD9A", id, true},
		{"https://mangadex.org/title/" + id + "/rokudenashi-blues", id, true},
		{"https://mangadex.org/title/" + id, id, true},
		{"https://www.mangadex.org/title/" + id + "/x?tab=chapters", id, true},
		{"http://mangadex.org/title/" + id + "/", id, true},
		{"https://example.org/title/" + id, "", false},
		{"https://mangadex.org/chapter/" + id, "", false},
		{"https://mangadex.org/title/not-a-uuid/x", "", false},
		{"https://mangadex.org.evil.example/title/" + id, "", false},
		{"javascript:alert(1)", "", false},
		{"rokudenashi blues", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		got, ok := ParseSeriesID(tt.in)
		if got != tt.want || ok != tt.ok {
			t.Errorf("ParseSeriesID(%q) = %q, %v; want %q, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}
