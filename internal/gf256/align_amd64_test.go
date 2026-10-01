//go:build !purego

package gf256

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestLoopsAligned checks that every loop of the amd64 kernels, a label that
// a later jump in the same function targets, is preceded by PCALIGN $64.
func TestLoopsAligned(t *testing.T) {
	src, err := os.ReadFile("kernels_amd64.s")
	if err != nil {
		t.Fatal(err)
	}
	label := regexp.MustCompile(`^([A-Za-z_]\w*):\s*$`)
	jump := regexp.MustCompile(`^\s+J[A-Z]+\s+([A-Za-z_]\w*)\s*$`)
	lines := strings.Split(string(src), "\n")
	var defined map[string]int
	loops := 0
	for i, l := range lines {
		if strings.HasPrefix(l, "TEXT ") {
			defined = map[string]int{}
			continue
		}
		if m := label.FindStringSubmatch(l); m != nil && defined != nil {
			defined[m[1]] = i
		}
		m := jump.FindStringSubmatch(l)
		if m == nil || defined == nil {
			continue
		}
		at, ok := defined[m[1]]
		if !ok {
			continue
		}
		loops++
		if at == 0 || strings.TrimSpace(lines[at-1]) != "PCALIGN $64" {
			t.Errorf("kernels_amd64.s:%d: loop %s has no PCALIGN $64 before it", at+1, m[1])
		}
		delete(defined, m[1])
	}
	if loops == 0 {
		t.Error("found no loops: the parser no longer matches kernels_amd64.s")
	}
}
