package mangadex

import (
	"encoding/json"
	"testing"
)

func TestParseAggregate(t *testing.T) {
	for name, raw := range map[string]string{
		"objects": `{"1":{"volume":"1","chapters":{"1":{"chapter":"1"},"2":{"chapter":"2"},"2.5":{"chapter":"2.5"}}},
			"2":{"volume":"2","chapters":{"3":{"chapter":"3"}}},
			"none":{"volume":"none","chapters":{"4":{"chapter":"4"},"none":{"chapter":"none"}}}}`,
		// PHP-style arrays when the keys are 0, 1, 2…
		"arrays": `[{"volume":"1","chapters":[{"chapter":"1"},{"chapter":"2"},{"chapter":"2.5"}]},
			{"volume":"2","chapters":{"3":{"chapter":"3"}}},
			{"volume":"none","chapters":[{"chapter":"4"}]}]`,
	} {
		got, err := parseAggregate(json.RawMessage(raw))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		want := map[string]string{"1": "1", "2": "1", "2.5": "1", "3": "2"}
		if len(got) != len(want) {
			t.Errorf("%s: got %d chapters, want %d", name, len(got), len(want))
		}
		for ch, vol := range want {
			if g := got[NewIdentifier(ch)]; g.String() != vol {
				t.Errorf("%s: chapter %s in volume %q, want %q", name, ch, g.String(), vol)
			}
		}
		if _, ok := got[NewIdentifier("4")]; ok {
			t.Errorf("%s: a chapter without volume must be left out", name)
		}
	}

	for _, empty := range []string{``, `null`, `[]`, `{}`} {
		if got, err := parseAggregate(json.RawMessage(empty)); err != nil || len(got) != 0 {
			t.Errorf("%q: got %v, %v; want nothing", empty, got, err)
		}
	}
	if _, err := parseAggregate(json.RawMessage(`"oops"`)); err == nil {
		t.Error("garbage must be an error")
	}
}
