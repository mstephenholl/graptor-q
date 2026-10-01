package main

import "testing"

const base = "github.com/mstephenholl/graptor-q"

func v(major, minor, patch int) *Version { return &Version{major, minor, patch} }

func TestNext(t *testing.T) {
	for _, c := range []struct {
		latest *Version
		level  Level
		module string
		want   string // version or "error"
	}{
		{nil, Patch, base, "v0.1.0"}, // the first release
		{nil, Minor, base, "v0.1.0"},
		{nil, Major, base, "v1.0.0"},
		{v(0, 1, 0), Patch, base, "v0.1.1"},
		{v(0, 1, 7), Minor, base, "v0.2.0"},
		{v(0, 9, 3), Major, base, "v1.0.0"}, // the compatibility promise
		{v(1, 4, 2), Minor, base, "v1.5.0"},
		{v(1, 4, 2), Major, base, "error"},          // v2 needs the /v2 path
		{v(1, 4, 2), Major, base + "/v2", "v2.0.0"}, // ... which the PR adds
		{v(1, 4, 2), Patch, base + "/v2", "error"},  // /v2 path without a major bump
		{v(2, 0, 0), Patch, base + "/v2", "v2.0.1"},
		{v(2, 0, 0), Major, base + "/v2", "error"}, // v3 needs /v3
		{v(0, 3, 0), Patch, base + "/v3", "error"},
	} {
		got, err := Next(c.latest, c.level, c.module, base)
		s := got.String()
		if err != nil {
			s = "error"
		}
		if s != c.want {
			t.Errorf("Next(%v, %v, %s) = %s (%v), want %s", c.latest, c.level, c.module, s, err, c.want)
		}
	}
}

func TestLevelFromLabels(t *testing.T) {
	for _, c := range []struct {
		labels []string
		want   Level
		err    bool
	}{
		{nil, Patch, false},
		{[]string{"perf:accept", "documentation"}, Patch, false},
		{[]string{"release:minor"}, Minor, false},
		{[]string{"bug", "release:major"}, Major, false},
		{[]string{"release:minor", "release:major"}, 0, true},
		{[]string{"release:breaking"}, 0, true},
	} {
		got, err := LevelFromLabels(c.labels)
		if (err != nil) != c.err || (err == nil && got != c.want) {
			t.Errorf("LevelFromLabels(%q) = %v, %v", c.labels, got, err)
		}
	}
}

func TestParseAPIDiff(t *testing.T) {
	for _, c := range []struct {
		report string
		want   string // change or "error"
	}{
		{"", "unchanged"},
		{"\n", "unchanged"},
		{"Compatible changes:\n- C: added\n- ./sub.T: added\n", "feature"},
		{"Incompatible changes:\n- B: changed from func(int) to func(string)\n" +
			"Compatible changes:\n- C: added\n- package example.com/m/newpkg: added\n", "breaking"},
		{"Incompatible changes:\n- package example.com/m/sub: removed\n", "breaking"},
		{"- C: added\n", "error"},                      // an entry without a section
		{"Additions:\n- C: added\n", "error"},          // a section this parser does not know
		{"Compatible changes:\n  C: added\n", "error"}, // an entry in another format
		{"loading example.com/m: m.go:3:12: expected ';'\n", "error"},
	} {
		got, err := ParseAPIDiff(c.report)
		s := got.String()
		if err != nil {
			s = "error"
		}
		if s != c.want {
			t.Errorf("ParseAPIDiff(%q) = %s (%v), want %s", c.report, s, err, c.want)
		}
	}
}

func TestMinLevel(t *testing.T) {
	for _, c := range []struct {
		latest *Version
		change APIChange
		want   Level
	}{
		{nil, APIUnchanged, Patch},
		{nil, APIFeature, Patch},
		{nil, APIBreaking, Minor},
		{v(0, 4, 2), APIFeature, Patch},
		{v(0, 4, 2), APIBreaking, Minor},
		{v(1, 0, 0), APIUnchanged, Patch},
		{v(1, 0, 0), APIFeature, Patch},
		{v(1, 0, 0), APIBreaking, Major},
		{v(2, 3, 1), APIBreaking, Major},
	} {
		if got := MinLevel(c.latest, c.change); got != c.want {
			t.Errorf("MinLevel(%v, %v) = %v, want %v", c.latest, c.change, got, c.want)
		}
	}
}

func TestParseVersion(t *testing.T) {
	for tag, ok := range map[string]bool{
		"v0.1.0": true, "v10.20.30": true,
		"v1.2": false, "1.2.3": false, "v1.2.3-rc.1": false, "v01.2.3": false, "interop/v0.1.0": false,
	} {
		if _, got := ParseVersion(tag); got != ok {
			t.Errorf("ParseVersion(%q) ok = %v, want %v", tag, got, ok)
		}
	}
	a, _ := ParseVersion("v0.10.0")
	b, _ := ParseVersion("v0.9.12")
	if !b.Less(a) || a.Less(b) {
		t.Error("v0.9.12 must sort before v0.10.0")
	}
}

func TestModulePath(t *testing.T) {
	got, err := modulePath("// comment\nmodule github.com/mstephenholl/graptor-q/v2\n\ngo 1.24\n")
	if err != nil || got != base+"/v2" {
		t.Errorf("modulePath = %q, %v", got, err)
	}
}
