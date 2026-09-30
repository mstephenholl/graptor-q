package testutil

import (
	"bufio"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"os"
)

// PatternData returns n bytes of the non-periodic test pattern shared with
// tools/rqoracle: byte i is the top byte of (i + seed) * 0x9E3779B97F4A7C15.
// Unlike VectorData it does not repeat every 256 bytes, so misplaced blocks
// or sub-blocks cannot go unnoticed.
func PatternData(n int, seed uint64) []byte {
	d := make([]byte, n)
	for i := range d {
		d[i] = byte((uint64(i) + seed) * 0x9E3779B97F4A7C15 >> 56)
	}
	return d
}

// OracleSymbol is one encoding symbol of an oracle vector.
type OracleSymbol struct {
	SBN  uint8  `json:"sbn"`
	ESI  uint32 `json:"esi"`
	Data string `json:"data"` // hex
}

// OracleVector is one line of a vectors.jsonl.gz file written by
// "rqoracle gen-vectors". Object vectors carry an OTI, the data seed and
// symbols; derive vectors carry the inputs and output of RFC 6330 Section 4.3
// as implemented by the oracle.
type OracleVector struct {
	Name    string         `json:"name"`
	OTI     string         `json:"oti"` // hex of the 12-byte OTI
	Seed    uint64         `json:"seed"`
	Decode  bool           `json:"decode"` // symbols suffice to decode the object
	Symbols []OracleSymbol `json:"symbols"`
	F       uint64         `json:"f"`   // derive only
	MTU     int            `json:"mtu"` // derive only
}

// LoadOracleVectors reads a gzip-compressed JSON-lines vector file.
func LoadOracleVectors(path string) ([]OracleVector, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }() // read-only: closing cannot lose data
	zr, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	var out []OracleVector
	sc := bufio.NewScanner(zr)
	sc.Buffer(nil, 64<<20)
	for sc.Scan() {
		var v OracleVector
		if err := json.Unmarshal(sc.Bytes(), &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, sc.Err()
}

// SplitPackets splits a stream of u32-BE length-prefixed packets (the format
// of "rqoracle encode") into packets.
func SplitPackets(b []byte) [][]byte {
	var pkts [][]byte
	for len(b) >= 4 {
		n := int(binary.BigEndian.Uint32(b))
		pkts = append(pkts, b[4:4+n])
		b = b[4+n:]
	}
	return pkts
}

// JoinPackets is the inverse of SplitPackets.
func JoinPackets(pkts [][]byte) []byte {
	var b []byte
	for _, p := range pkts {
		b = binary.BigEndian.AppendUint32(b, uint32(len(p)))
		b = append(b, p...)
	}
	return b
}
