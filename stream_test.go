package graptorq

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/mholland/graptorq/internal/testutil"
)

// memWriterAt is an in-memory io.WriterAt. Writes fail while failures > 0.
type memWriterAt struct {
	mu       sync.Mutex
	buf      []byte
	writes   int
	failures int
}

var errInjected = errors.New("injected I/O error")

func (m *memWriterAt) WriteAt(p []byte, off int64) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failures > 0 {
		m.failures--
		return 0, errInjected
	}
	if end := int(off) + len(p); end > len(m.buf) {
		m.buf = append(m.buf, make([]byte, end-len(m.buf))...)
	}
	copy(m.buf[off:], p)
	m.writes++
	return len(p), nil
}

// failingReaderAt fails reads that reach offset failFrom or beyond.
type failingReaderAt struct {
	r        io.ReaderAt
	failFrom int64
}

func (f failingReaderAt) ReadAt(p []byte, off int64) (int, error) {
	if off+int64(len(p)) > f.failFrom {
		return 0, errInjected
	}
	return f.r.ReadAt(p, off)
}

// residentBlocks returns how many blocks the encoder holds.
func (e *Encoder) residentBlocks() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	n := 0
	for _, b := range e.blocks {
		if b != nil {
			n++
		}
	}
	if n != e.resident {
		panic("resident count out of sync")
	}
	return n
}

func randomData(rng *rand.Rand, n uint64) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(rng.Uint32())
	}
	return b
}

// An encoder reading its object through an io.ReaderAt must produce the same
// symbols as one holding it in memory, whatever blocks it evicts.
func TestEncoderReaderAtMatchesBytes(t *testing.T) {
	rng := rand.New(rand.NewPCG(91, 92))
	for trial := range 25 {
		oti := randomOTI(rng)
		data := randomData(rng, oti.TransferLength)
		ref, err := NewEncoder(data, oti)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range []struct {
			opts  []Option
			limit int
		}{
			{nil, 1}, // the default for NewEncoderReaderAt
			{[]Option{WithBlockCache(2)}, 2},
			{[]Option{WithBlockCache(0)}, 0},
		} {
			enc, err := NewEncoderReaderAt(bytes.NewReader(data), oti, c.opts...)
			if err != nil {
				t.Fatal(err)
			}
			l := enc.Layout()
			for range 150 {
				sbn := uint8(rng.IntN(l.SourceBlocks()))
				id := PayloadID{sbn, uint32(rng.IntN(l.Block(sbn).K + 20))}
				got, err := enc.AppendSymbol(nil, id)
				if err != nil {
					t.Fatal(err)
				}
				want, _ := ref.AppendSymbol(nil, id)
				if !bytes.Equal(got, want) {
					t.Fatalf("trial %d %+v cache %d: symbol %+v differs", trial, oti, c.limit, id)
				}
				if n := enc.residentBlocks(); c.limit > 0 && n > c.limit {
					t.Fatalf("%d blocks resident, limit %d", n, c.limit)
				}
			}
			var gotAll, wantAll [][]byte
			for _, sym := range enc.Packets(3) {
				gotAll = append(gotAll, bytes.Clone(sym))
			}
			for _, sym := range ref.Packets(3) {
				wantAll = append(wantAll, bytes.Clone(sym))
			}
			if len(gotAll) != len(wantAll) || enc.Err() != nil {
				t.Fatalf("Packets: %d symbols, want %d (err %v)", len(gotAll), len(wantAll), enc.Err())
			}
			for i := range gotAll {
				if !bytes.Equal(gotAll[i], wantAll[i]) {
					t.Fatalf("Packets symbol %d differs", i)
				}
			}
		}
	}
}

func TestEncoderReaderAtErrors(t *testing.T) {
	rng := rand.New(rand.NewPCG(93, 94))
	oti := OTI{TransferLength: 30000, SymbolSize: 64, SourceBlocks: 3, SubBlocks: 1, Alignment: 4}
	data := randomData(rng, oti.TransferLength)
	l, _ := oti.Layout()
	second := l.Block(1)

	enc, _ := NewEncoderReaderAt(failingReaderAt{bytes.NewReader(data), int64(second.Offset)}, oti)
	if _, err := enc.AppendSymbol(nil, PayloadID{0, 5}); err != nil {
		t.Fatalf("block 0: %v", err)
	}
	if _, err := enc.AppendSymbol(nil, PayloadID{1, 5}); !errors.Is(err, errInjected) {
		t.Fatalf("block 1: err = %v, want the read error", err)
	}
	n := 0
	for range enc.Packets(2) {
		n++
	}
	if n != l.Block(0).K+2 || !errors.Is(enc.Err(), errInjected) {
		t.Fatalf("Packets yielded %d symbols (want %d), Err = %v", n, l.Block(0).K+2, enc.Err())
	}

	short, _ := NewEncoderReaderAt(bytes.NewReader(data[:len(data)-1]), oti)
	if _, err := short.AppendSymbol(nil, PayloadID{2, 0}); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("short source: err = %v, want io.ErrUnexpectedEOF", err)
	}
}

// Many goroutines using a one-block cache (run with -race).
func TestEncoderReaderAtConcurrent(t *testing.T) {
	rng := rand.New(rand.NewPCG(95, 96))
	oti := OTI{TransferLength: 50000, SymbolSize: 32, SourceBlocks: 5, SubBlocks: 2, Alignment: 4}
	data := randomData(rng, oti.TransferLength)
	ref, _ := NewEncoder(data, oti)
	enc, _ := NewEncoderReaderAt(bytes.NewReader(data), oti)
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := rand.New(rand.NewPCG(uint64(g), 97))
			for range 40 {
				id := PayloadID{uint8(r.IntN(5)), uint32(r.IntN(400))}
				got, err := enc.AppendSymbol(nil, id)
				want, _ := ref.AppendSymbol(nil, id)
				if err != nil || !bytes.Equal(got, want) {
					t.Errorf("symbol %+v: %v", id, err)
					return
				}
			}
		}()
	}
	wg.Wait()
}

// feedObject sends the packets of enc to dec with the given loss rate, then
// repair packets until dec is done (with deferred decoding, it calls Decode).
func feedObject(t *testing.T, rng *rand.Rand, enc *Encoder, dec *Decoder, loss float64, deferred bool) {
	t.Helper()
	l := enc.Layout()
	next := make([]uint32, l.SourceBlocks())
	for sbn := range l.SourceBlocks() {
		K := l.Block(uint8(sbn)).K
		next[sbn] = uint32(K)
		for esi := range uint32(K) {
			if rng.Float64() < loss {
				continue
			}
			pkt, _ := enc.AppendPacket(nil, PayloadID{uint8(sbn), esi})
			if _, err := dec.AddPacket(pkt); err != nil {
				t.Fatal(err)
			}
		}
		for b, _ := dec.Block(uint8(sbn)); b.Received() < K+2 && !b.Decoded(); next[sbn]++ {
			pkt, _ := enc.AppendPacket(nil, PayloadID{uint8(sbn), next[sbn]})
			if _, err := dec.AddPacket(pkt); err != nil {
				t.Fatal(err)
			}
		}
	}
	if deferred {
		if err := dec.Decode(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDecoderWriterAt(t *testing.T) {
	rng := rand.New(rand.NewPCG(97, 98))
	for trial := range 20 {
		oti := randomOTI(rng)
		data := randomData(rng, oti.TransferLength)
		enc, _ := NewEncoder(data, oti)
		w := &memWriterAt{}
		dec, err := NewDecoderWriterAt(w, oti)
		if err != nil {
			t.Fatal(err)
		}
		deferred := trial%2 == 1
		dec.SetDeferredDecode(deferred)
		for round := range 2 { // the second round checks Reset
			w.buf = w.buf[:0]
			feedObject(t, rng, enc, dec, 0.2, deferred)
			if !dec.Decoded() || !bytes.Equal(w.buf, data) {
				t.Fatalf("trial %d round %d %+v: object not written correctly", trial, round, oti)
			}
			for i, b := range dec.blocks {
				if !b.released || b.arena != nil || b.work != nil || !dec.written[i] {
					t.Fatalf("block %d not released after writing", i)
				}
			}
			if _, err := dec.AppendObject(nil); !errors.Is(err, ErrStreamed) {
				t.Fatalf("AppendObject: %v", err)
			}
			if _, err := dec.WriteTo(io.Discard); !errors.Is(err, ErrStreamed) {
				t.Fatalf("WriteTo: %v", err)
			}
			dec.Reset()
		}
	}
}

// A failed write is reported, and Decode writes the block again.
func TestDecoderWriterAtRetry(t *testing.T) {
	rng := rand.New(rand.NewPCG(99, 100))
	oti := OTI{TransferLength: 20000, SymbolSize: 64, SourceBlocks: 2, SubBlocks: 1, Alignment: 4}
	data := randomData(rng, oti.TransferLength)
	enc, _ := NewEncoder(data, oti)
	w := &memWriterAt{failures: 1}
	dec, _ := NewDecoderWriterAt(w, oti)
	var sawErr error
	for id, sym := range enc.Packets(0) {
		if _, err := dec.AddSymbol(id, sym); err != nil {
			if !errors.Is(err, errInjected) {
				t.Fatal(err)
			}
			sawErr = err
		}
	}
	if sawErr == nil || dec.Decoded() {
		t.Fatalf("write error not reported (err %v, decoded %v)", sawErr, dec.Decoded())
	}
	if err := dec.Decode(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !dec.Decoded() || !bytes.Equal(w.buf, data) {
		t.Fatal("object not written after retry")
	}
}

// Streaming a 64 MB file through NewEncoderReaderAt and NewDecoderWriterAt
// must hold only a block or two in memory, not the object.
func TestStreamingMemoryBound(t *testing.T) {
	if testing.Short() {
		t.Skip("streams a 64 MB file")
	}
	const F, chunk = 64 << 20, 1 << 20
	dir := t.TempDir()
	src, err := os.Create(filepath.Join(dir, "src"))
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	for off := 0; off < F; off += chunk {
		if _, err := src.Write(testutil.PatternData(chunk, uint64(off))); err != nil {
			t.Fatal(err)
		}
	}
	dst, err := os.Create(filepath.Join(dir, "dst"))
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()

	oti := OTI{TransferLength: F, SymbolSize: 1024, SourceBlocks: 64, SubBlocks: 1, Alignment: 8}
	enc, err := NewEncoderReaderAt(src, oti)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := NewDecoderWriterAt(dst, oti)
	if err != nil {
		t.Fatal(err)
	}
	// The live heap is sampled once per block; the growth over the heap
	// before streaming (which holds whatever earlier tests left, such as
	// cached plans) is what streaming holds.
	liveHeap := func() uint64 {
		runtime.GC()
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		return ms.HeapAlloc
	}
	base := liveHeap()
	K := enc.Layout().KL
	var peak uint64
	last := -1
	for id, sym := range enc.Packets(K/10 + 5) {
		if int(id.SBN) != last {
			if h := liveHeap(); h > base {
				peak = max(peak, h-base)
			}
			last = int(id.SBN)
		}
		if int(id.ESI) < K && id.ESI%10 == 3 {
			continue // lose 10% of the source symbols
		}
		if _, err := dec.AddSymbol(id, sym); err != nil {
			t.Fatal(err)
		}
	}
	if err := enc.Err(); err != nil || !dec.Decoded() {
		t.Fatalf("streaming failed: %v, decoded %v", err, dec.Decoded())
	}
	if peak > 16<<20 {
		t.Errorf("heap grew by %d MB while streaming a %d MB object", peak>>20, F>>20)
	}
	t.Logf("heap grew by at most %.1f MB while streaming a %d MB object", float64(peak)/(1<<20), F>>20)

	for off := int64(0); off < F; off += chunk {
		a, b := make([]byte, chunk), make([]byte, chunk)
		src.ReadAt(a, off)
		dst.ReadAt(b, off)
		if !bytes.Equal(a, b) {
			t.Fatalf("output differs at %d", off)
		}
	}
}
