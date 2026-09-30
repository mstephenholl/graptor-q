// Package rfctext extracts the numeric tables embedded in the plain-text
// publication of RFC 6330.
//
// It is used by the table generator (internal/rfc/gentables) and by tests that
// check the generated Go tables, and the GF(256) arithmetic, against the RFC
// text itself rather than against a hand transcription.
package rfctext

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// SystematicRow is one row of RFC 6330 Table 2 (Section 5.6).
type SystematicRow struct {
	KPrime, J, S, H, W int
}

// Tables holds every numeric table defined by RFC 6330.
type Tables struct {
	V          [4][256]uint32  // Section 5.5, V0..V3
	DegF       [31]uint32      // Section 5.3.5.2, Table 1: f[0..30]
	Systematic []SystematicRow // Section 5.6, Table 2
	OctExp     [510]byte       // Section 5.7.3
	OctLog     [256]byte       // Section 5.7.4; index 0 is unused (log of 0 is undefined)
}

var (
	headingRE    = regexp.MustCompile(`^\d+(\.\d+)*\.\s`)
	numberLineRE = regexp.MustCompile(`^\s*\d+(\s*,\s*\d+)*\s*,?\s*$`)
)

// Parse extracts all tables from the text of RFC 6330.
func Parse(text []byte) (*Tables, error) {
	lines := strings.Split(string(bytes.ReplaceAll(text, []byte("\f"), nil)), "\n")
	t := new(Tables)

	for i, name := range []string{"5.5.1.", "5.5.2.", "5.5.3.", "5.5.4."} {
		nums, err := numberList(lines, name)
		if err != nil {
			return nil, err
		}
		if len(nums) != 256 {
			return nil, fmt.Errorf("rfctext: table V%d has %d entries, want 256", i, len(nums))
		}
		for j, n := range nums {
			if n > 0xFFFFFFFF {
				return nil, fmt.Errorf("rfctext: V%d[%d] = %d overflows uint32", i, j, n)
			}
			t.V[i][j] = uint32(n)
		}
	}

	if err := parseDegree(lines, t); err != nil {
		return nil, err
	}
	if err := parseSystematic(lines, t); err != nil {
		return nil, err
	}

	exp, err := numberList(lines, "5.7.3.")
	if err != nil {
		return nil, err
	}
	if len(exp) != len(t.OctExp) {
		return nil, fmt.Errorf("rfctext: OCT_EXP has %d entries, want %d", len(exp), len(t.OctExp))
	}
	for i, n := range exp {
		if n > 255 {
			return nil, fmt.Errorf("rfctext: OCT_EXP[%d] = %d is not an octet", i, n)
		}
		t.OctExp[i] = byte(n)
	}

	log, err := numberList(lines, "5.7.4.")
	if err != nil {
		return nil, err
	}
	if len(log) != 255 {
		return nil, fmt.Errorf("rfctext: OCT_LOG has %d entries, want 255", len(log))
	}
	for i, n := range log {
		if n > 254 {
			return nil, fmt.Errorf("rfctext: OCT_LOG[%d] = %d out of range", i+1, n)
		}
		t.OctLog[i+1] = byte(n)
	}
	return t, nil
}

// section returns the body lines of the section whose heading starts with
// prefix at column 0 (table-of-contents entries are indented, so they never
// match), up to the next heading.
func section(lines []string, prefix string) ([]string, error) {
	start := -1
	for i, l := range lines {
		if strings.HasPrefix(l, prefix+" ") {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return nil, fmt.Errorf("rfctext: section %s not found", prefix)
	}
	end := len(lines)
	for i := start; i < len(lines); i++ {
		if headingRE.MatchString(lines[i]) {
			end = i
			break
		}
	}
	return lines[start:end], nil
}

// numberList collects the integers from all lines of a section that consist
// solely of comma-separated numbers. Prose lines and page headers/footers
// always contain letters, so they are skipped.
func numberList(lines []string, prefix string) ([]uint64, error) {
	body, err := section(lines, prefix)
	if err != nil {
		return nil, err
	}
	var out []uint64
	for _, l := range body {
		if !numberLineRE.MatchString(l) {
			continue
		}
		for _, f := range strings.Split(l, ",") {
			f = strings.TrimSpace(f)
			if f == "" {
				continue
			}
			n, err := strconv.ParseUint(f, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("rfctext: section %s: %w", prefix, err)
			}
			out = append(out, n)
		}
	}
	return out, nil
}

// tableRows returns the numeric cells of every "| a | b | ..." row in a
// section. Header rows (non-numeric cells) are skipped; empty cells are dropped.
func tableRows(lines []string, prefix string) ([][]int, error) {
	body, err := section(lines, prefix)
	if err != nil {
		return nil, err
	}
	var rows [][]int
	for _, l := range body {
		l = strings.TrimSpace(l)
		if !strings.HasPrefix(l, "|") {
			continue
		}
		var row []int
		numeric := true
		for _, cell := range strings.Split(strings.Trim(l, "|"), "|") {
			cell = strings.TrimSpace(cell)
			if cell == "" {
				continue
			}
			n, err := strconv.Atoi(cell)
			if err != nil {
				numeric = false
				break
			}
			row = append(row, n)
		}
		if numeric && len(row) > 0 {
			rows = append(rows, row)
		}
	}
	return rows, nil
}

func parseDegree(lines []string, t *Tables) error {
	rows, err := tableRows(lines, "5.3.5.2.")
	if err != nil {
		return err
	}
	seen := 0
	for _, r := range rows {
		if len(r)%2 != 0 {
			return fmt.Errorf("rfctext: Table 1 row %v has an odd number of cells", r)
		}
		for i := 0; i < len(r); i += 2 {
			d, f := r[i], r[i+1]
			if d != seen {
				return fmt.Errorf("rfctext: Table 1 index %d out of order (want %d)", d, seen)
			}
			if d >= len(t.DegF) {
				return fmt.Errorf("rfctext: Table 1 index %d out of range", d)
			}
			t.DegF[d] = uint32(f)
			seen++
		}
	}
	if seen != len(t.DegF) {
		return fmt.Errorf("rfctext: Table 1 has %d entries, want %d", seen, len(t.DegF))
	}
	return nil
}

func parseSystematic(lines []string, t *Tables) error {
	rows, err := tableRows(lines, "5.6.")
	if err != nil {
		return err
	}
	for _, r := range rows {
		if len(r) != 5 {
			return fmt.Errorf("rfctext: Table 2 row %v has %d cells, want 5", r, len(r))
		}
		t.Systematic = append(t.Systematic, SystematicRow{KPrime: r[0], J: r[1], S: r[2], H: r[3], W: r[4]})
	}
	if len(t.Systematic) != 477 {
		return fmt.Errorf("rfctext: Table 2 has %d rows, want 477", len(t.Systematic))
	}
	return nil
}
