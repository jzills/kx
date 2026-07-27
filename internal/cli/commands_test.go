package cli

import "testing"

// The leading run of numbers are indexes; everything after is kubectl's.
func TestSplitLeadingIndexes(t *testing.T) {
	cases := []struct {
		args        []string
		wantIndexes int
		wantRest    string
	}{
		{[]string{"1", "2", "3"}, 3, ""},
		{[]string{"1", "--show-events=false"}, 1, "--show-events=false"},
		{[]string{"1", "2", "-o", "wide"}, 2, "-o wide"},
		{[]string{"--flag", "1"}, 0, "--flag 1"},
		{nil, 0, ""},
	}
	for _, tc := range cases {
		indexes, rest := splitLeadingIndexes(tc.args)
		if len(indexes) != tc.wantIndexes {
			t.Errorf("splitLeadingIndexes(%v) indexes = %v, want %d", tc.args, indexes, tc.wantIndexes)
		}
		if joinArgs(rest) != tc.wantRest {
			t.Errorf("splitLeadingIndexes(%v) rest = %q, want %q", tc.args, joinArgs(rest), tc.wantRest)
		}
	}
}

func TestSplitAtDoubleDash(t *testing.T) {
	before, after := splitAtDoubleDash([]string{"1", "-c", "app", "--", "ls", "/x"})
	if joinArgs(before) != "1 -c app" {
		t.Errorf("before = %q", joinArgs(before))
	}
	if joinArgs(after) != "ls /x" {
		t.Errorf("after = %q", joinArgs(after))
	}

	before, after = splitAtDoubleDash([]string{"1"})
	if joinArgs(before) != "1" || after != nil {
		t.Errorf("no separator: before = %q, after = %v", joinArgs(before), after)
	}
}

func TestParseIndexNamesTheArgument(t *testing.T) {
	_, err := parseIndex("indexes", "abc")
	if err == nil {
		t.Fatal("parseIndex accepted a non-integer")
	}
	want := "Invalid value for 'indexes': 'abc' is not a valid int."
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}
}

// Following several pods in turn would block on the first and never reach the
// rest, so it is refused rather than half-done.
func TestCheckFollow(t *testing.T) {
	cases := []struct {
		args    []string
		indexes int
		wantErr bool
	}{
		{[]string{"-f"}, 1, false},
		{[]string{"--follow"}, 1, false},
		{[]string{"-f"}, 2, true},
		{[]string{"--follow"}, 3, true},
		{[]string{"--tail=10"}, 3, false},
		{nil, 3, false},
	}
	for _, tc := range cases {
		err := checkFollow(tc.args, tc.indexes)
		if (err != nil) != tc.wantErr {
			t.Errorf("checkFollow(%v, %d) = %v, wantErr %v", tc.args, tc.indexes, err, tc.wantErr)
		}
	}
}
