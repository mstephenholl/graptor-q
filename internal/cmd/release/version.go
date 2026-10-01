package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Version is a release version vMAJOR.MINOR.PATCH. Release tags carry no
// pre-release or build suffix.
type Version struct{ Major, Minor, Patch int }

var versionTag = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

// ParseVersion parses a release tag. Other tags (such as v1.2.3-rc.1 or
// interop/v0.1.0) are not release tags and report false.
func ParseVersion(tag string) (Version, bool) {
	m := versionTag.FindStringSubmatch(tag)
	if m == nil {
		return Version{}, false
	}
	var n [3]int
	for i := range n {
		v, err := strconv.Atoi(m[i+1])
		if err != nil {
			return Version{}, false
		}
		n[i] = v
	}
	return Version{n[0], n[1], n[2]}, true
}

func (v Version) String() string { return fmt.Sprintf("v%d.%d.%d", v.Major, v.Minor, v.Patch) }

// Less reports whether v sorts before w.
func (v Version) Less(w Version) bool {
	if v.Major != w.Major {
		return v.Major < w.Major
	}
	if v.Minor != w.Minor {
		return v.Minor < w.Minor
	}
	return v.Patch < w.Patch
}

// Level is the part of the version a change increments.
type Level int

// Levels, in increasing order.
const (
	Patch Level = iota
	Minor
	Major
)

func (l Level) String() string { return [...]string{"patch", "minor", "major"}[l] }

const labelPrefix = "release:"

// LevelFromLabels returns the level a pull request's labels ask for, Patch
// without a release label. Two release labels or an unknown one are an error,
// not a guess: the module proxy keeps a version once it has served it.
func LevelFromLabels(labels []string) (Level, error) {
	level, found := Patch, ""
	for _, l := range labels {
		name, ok := strings.CutPrefix(l, labelPrefix)
		if !ok {
			continue
		}
		if found != "" {
			return 0, fmt.Errorf("labels %q and %q both set the release level; keep one", found, l)
		}
		found = l
		switch name {
		case "patch":
			level = Patch
		case "minor":
			level = Minor
		case "major":
			level = Major
		default:
			return 0, fmt.Errorf("unknown release label %q (use release:major, release:minor or release:patch)", l)
		}
	}
	return level, nil
}

// APIChange is how the exported API of the module changed between two
// commits, as golang.org/x/exp/cmd/apidiff reports it.
type APIChange int

// API changes, in increasing order.
const (
	APIUnchanged APIChange = iota
	APIFeature             // compatible changes only, such as additions
	APIBreaking            // at least one incompatible change
)

func (c APIChange) String() string { return [...]string{"unchanged", "feature", "breaking"}[c] }

// ParseAPIDiff classifies the text report of apidiff -m by its "Incompatible
// changes:" and "Compatible changes:" sections. Any other line is an error,
// so that an unknown format cannot let an incompatible change through.
func ParseAPIDiff(report string) (APIChange, error) {
	report = strings.TrimSpace(report)
	if report == "" {
		return APIUnchanged, nil
	}
	change := APIUnchanged
	for line := range strings.SplitSeq(report, "\n") {
		switch line {
		case "Incompatible changes:":
			change = APIBreaking
		case "Compatible changes:":
			change = max(change, APIFeature)
		default:
			if change == APIUnchanged || !strings.HasPrefix(line, "- ") {
				return 0, fmt.Errorf("unrecognized apidiff output %q", line)
			}
		}
	}
	return change, nil
}

// MinLevel returns the lowest level a pull request with the given API
// change may be released at, after latest (nil before the first release):
// Minor for an incompatible change in v0, where Go promises nothing, Major
// from v1 on.
func MinLevel(latest *Version, change APIChange) Level {
	switch {
	case change != APIBreaking:
		return Patch
	case latest == nil || latest.Major == 0:
		return Minor
	}
	return Major
}

// Next returns the version after latest (nil before the first release) for
// a change of the given level: v0.1.0 first, and v1.0.0 for Major in v0. It
// fails when modulePath breaks Go's /vN rule for that version, which the go
// command could not fetch; modBase is the module path without /vN.
func Next(latest *Version, level Level, modulePath, modBase string) (Version, error) {
	var next Version
	switch {
	case latest == nil && level == Major:
		next = Version{1, 0, 0}
	case latest == nil:
		next = Version{0, 1, 0}
	case level == Major:
		next = Version{latest.Major + 1, 0, 0}
	case level == Minor:
		next = Version{latest.Major, latest.Minor + 1, 0}
	default:
		next = Version{latest.Major, latest.Minor, latest.Patch + 1}
	}
	if want := ModuleFor(modBase, next); modulePath != want {
		return Version{}, fmt.Errorf("go.mod declares module %s, but %s needs module %s", modulePath, next, want)
	}
	return next, nil
}

// ModuleFor returns the module path version v is published under: modBase
// for v0 and v1, modBase/vN from v2 on.
func ModuleFor(modBase string, v Version) string {
	if v.Major >= 2 {
		return fmt.Sprintf("%s/v%d", modBase, v.Major)
	}
	return modBase
}

// modulePath returns the module path declared by a go.mod file.
func modulePath(gomod string) (string, error) {
	for line := range strings.SplitSeq(gomod, "\n") {
		if p, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			return strings.Trim(strings.TrimSpace(p), `"`), nil
		}
	}
	return "", fmt.Errorf("go.mod has no module line")
}
