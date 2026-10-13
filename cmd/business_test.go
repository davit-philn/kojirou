package cmd

import "testing"

func TestOptionsAcceptMangaDexLinks(t *testing.T) {
	const id = "c727d921-4294-455b-9bcc-2f0c7d29cd9a"
	t.Cleanup(func() { identifierArg, sourceConfigArg = "", "" })

	for _, in := range []string{
		id,
		"https://mangadex.org/title/" + id + "/rokudenashi-blues",
	} {
		identifierArg, sourceConfigArg = in, ""
		opts, err := optionsFromFlags()
		if err != nil {
			t.Fatal(err)
		}
		if opts.Identifier != id {
			t.Errorf("%q -> %q, want %q", in, opts.Identifier, id)
		}
	}

	// anything else is passed through unchanged
	identifierArg = "12345"
	if opts, _ := optionsFromFlags(); opts.Identifier != "12345" {
		t.Errorf("other identifiers must not be changed, got %q", opts.Identifier)
	}
}
