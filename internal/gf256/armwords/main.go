// Command armwords writes the encodings of the NEON instructions in a 32-bit
// ARM assembly file. Go's assembler has no NEON instructions for GOARCH=arm,
// so each one is a WORD whose comment is its GNU mnemonic:
//
//	WORD $0xf421020d // vld1.8 {d0-d3}, [r1]!
//
// armwords assembles each such mnemonic with the GNU assembler and rewrites
// the WORD's value, so that no encoding is written by hand.
//
//	armwords [-as arm-linux-gnueabihf-as] kernels_arm.s
package main

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// wordLine splits a WORD line into the text before the value, the value, and
// the comment, whose text after "//" is the mnemonic.
var wordLine = regexp.MustCompile(`^(\s*WORD\s+\$)(\S+)(\s*//\s*(.*\S))\s*$`)

func main() {
	as := flag.String("as", "arm-linux-gnueabihf-as", "GNU assembler for 32-bit ARM")
	flag.Parse()
	if flag.NArg() != 1 {
		log.Fatal("usage: armwords [-as assembler] file.s")
	}
	if err := rewrite(*as, flag.Arg(0)); err != nil {
		log.Fatal(err)
	}
}

func rewrite(as, path string) error {
	src, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "armwords")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()

	lines := strings.Split(string(src), "\n")
	for i, l := range lines {
		m := wordLine.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		w, err := assemble(as, filepath.Join(dir, "word.o"), m[4])
		if err != nil {
			return fmt.Errorf("%s:%d: %w", path, i+1, err)
		}
		lines[i] = fmt.Sprintf("%s0x%08x%s", m[1], w, m[3])
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644)
}

// assemble returns the encoding of one ARM-mode instruction, which must
// assemble to exactly 4 bytes.
func assemble(as, obj, mnemonic string) (uint32, error) {
	cmd := exec.Command(as, "-march=armv7-a", "-mfpu=neon", "-o", obj)
	cmd.Stdin = strings.NewReader(".syntax unified\n.arm\n" + mnemonic + "\n")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return 0, fmt.Errorf("%s: %w: %s", mnemonic, err, stderr.Bytes())
	}
	f, err := elf.Open(obj)
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }() // read-only: closing cannot lose data
	s := f.Section(".text")
	if s == nil {
		return 0, fmt.Errorf("%s: no .text section", mnemonic)
	}
	text, err := s.Data()
	if err != nil {
		return 0, err
	}
	if len(text) != 4 {
		return 0, fmt.Errorf("%s: assembles to %d bytes, not one instruction", mnemonic, len(text))
	}
	return binary.LittleEndian.Uint32(text), nil
}
