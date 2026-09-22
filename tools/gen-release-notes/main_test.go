package main

import "testing"

// The previous tag is the range the notes cover, so it has to be the highest
// tag *below* the release being built. Taking the first entry of a descending
// tag list is only that while releases are strictly linear: cutting a patch on
// an older line picks up the newer minor above it, `git log v0.6.0..v0.5.3`
// resolves to nothing, and the notes ship with an empty middle tier and no
// error to say so.
func TestPriorTagIsTheHighestTagBelow(t *testing.T) {
	// As `git tag --sort=-v:refname` hands them over.
	tags := []string{"v0.6.0", "v0.5.3", "v0.5.2", "v0.5.1", "v0.4.0"}

	for _, c := range []struct {
		tag  string
		want string
	}{
		{"v0.6.0", "v0.5.3"},
		{"v0.5.3", "v0.5.2"},
		{"v0.5.2", "v0.5.1"},
		// A tag that does not exist yet — the ordinary case, since the notes
		// are generated for a release being cut.
		{"v0.5.4", "v0.5.3"},
		{"v0.7.0", "v0.6.0"},
	} {
		got, err := priorTag(c.tag, tags)
		if err != nil {
			t.Errorf("priorTag(%s): %v", c.tag, err)
			continue
		}
		if got != c.want {
			t.Errorf("priorTag(%s) = %s, want %s", c.tag, got, c.want)
		}
	}
}

// The first release has nothing below it, and an empty range would silently
// produce notes covering the whole history.
func TestPriorTagRefusesWhenNothingIsBelow(t *testing.T) {
	if got, err := priorTag("v0.0.1", []string{"v0.1.0", "v0.0.2"}); err == nil {
		t.Errorf("priorTag = %q, want an error — nothing is below v0.0.1", got)
	}
}

// Tags that are not releases sit alongside the ones that are; they are not
// candidates for a range.
func TestPriorTagIgnoresUnparseableTags(t *testing.T) {
	got, err := priorTag("v0.5.3", []string{"nightly", "v0.5.2", "site-2026-01"})
	if err != nil {
		t.Fatalf("priorTag: %v", err)
	}
	if got != "v0.5.2" {
		t.Errorf("priorTag = %s, want v0.5.2", got)
	}
}
