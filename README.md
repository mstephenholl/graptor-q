# graptor-q

graptor-q is a pure Go implementation of RaptorQ
([RFC 6330](https://www.rfc-editor.org/rfc/rfc6330)), a forward error
correction code for channels that lose packets. The sender splits an object
into source symbols and adds repair symbols to cover the expected loss.
The receiver recovers each source block of K symbols from almost any K
symbols that arrive, and from K+2 with a probability above 99.9999%.

graptor-q implements the whole RFC, including the object-delivery layer, so
it interoperates with other RFC 6330 implementations. It has no
dependencies outside the standard library, and it uses SIMD kernels on
amd64 and arm64.

## Install

```sh
go get github.com/mstephenholl/graptor-q
```

graptor-q needs Go 1.24 or later. The package name is `graptorq`.

The repository is private. Set `GOPRIVATE=github.com/mstephenholl/*` before
you run `go get`, so that Go fetches the module from GitHub with your Git
credentials instead of through the public module proxy.

## Quick start

The sender derives the Object Transmission Information (OTI) from the object
size and sends its 12 bytes to the receiver first. Each packet after that
holds a 4-byte FEC Payload ID and one symbol.

```go
import (
	"errors"

	"github.com/mstephenholl/graptor-q"
)

func sendObject(object []byte, repair int, send func([]byte)) error {
	oti, err := graptorq.DeriveOTI(uint64(len(object)), graptorq.Config{PayloadSize: 1024})
	if err != nil {
		return err
	}
	enc, err := graptorq.NewEncoder(object, oti)
	if err != nil {
		return err
	}
	header, _ := oti.MarshalBinary()
	send(header)
	for id, symbol := range enc.Packets(repair) {
		pkt, _ := id.AppendBinary(nil)
		send(append(pkt, symbol...))
	}
	return enc.Err()
}

func receiveObject(header []byte, packets <-chan []byte) ([]byte, error) {
	oti, err := graptorq.ParseOTI(header)
	if err != nil {
		return nil, err
	}
	dec, err := graptorq.NewDecoder(oti)
	if err != nil {
		return nil, err
	}
	for pkt := range packets {
		if done, err := dec.AddPacket(pkt); err != nil {
			return nil, err
		} else if done {
			return dec.AppendObject(nil)
		}
	}
	return nil, errors.New("not enough packets arrived")
}
```

`PayloadSize` is the symbol size T, so each packet is T+4 bytes. `repair`
counts repair symbols per source block. Set it to the number of packets per
block that you expect to lose, plus 2. `enc.Layout().KL` is the number of
source symbols in the largest blocks.

The receiver needs the OTI before its first packet. This example sends it as
the first message. A protocol can also carry it separately, for example in a
session description.

`example_test.go` has runnable versions of this flow (`Example`), of
streaming a file larger than memory (`ExampleNewDecoderWriterAt`), and of
encoding a single block (`ExampleBlockEncoder`).

## Choose an API

| To | Use |
|---|---|
| Send an object as packets that any RFC 6330 receiver can decode | `DeriveOTI`, `NewEncoder` and `NewDecoder` |
| Send a file larger than memory | `NewEncoderReaderAt` and `NewDecoderWriterAt` |
| Protect one block of data inside your own packet format | `NewBlockEncoder` and `NewBlockDecoder` |

The streaming constructors take an `io.ReaderAt` and an `io.WriterAt`, such
as an `*os.File`. The encoder reads one source block at a time, and the
decoder writes each block as soon as it decodes, then frees its memory.
Memory use follows the size of a source block, not of the object. Streaming
a 64 MB file in 64 source blocks of 1 MB grows the heap by about 1.5 MB. For
smaller blocks, build the OTI yourself, as `ExampleNewDecoderWriterAt` does.
An object holds at most 255 source blocks of 56,403 symbols, which is about
14.7 GB at T = 1024.

`Encoder.AppendSymbols` packs several consecutive symbols into one packet
(RFC 6330 §4.4.2). `BlockEncoder.AppendRepair` appends repair symbols by
repair index, so sending more repair for a block needs no ESI arithmetic.
With the block API, your packets carry each symbol's ESI, and the receiver
needs the block length and symbol size.

## Options

Pass options to the constructors. The defaults suit trusted input.

| Option | Use it to |
|---|---|
| `WithMaxOverhead(n)` | Make a decoder store at most K+n symbols per block. Set it, with n ≥ 2, when packets come from an untrusted source. |
| `WithMaxMemory(bytes)` | Make blocks that need more working memory fail with `ErrMemoryLimit`. Set it in decoders when the OTI comes from an untrusted source. |
| `WithConcurrency(n)` | Use at most n goroutines. The default is GOMAXPROCS, and 1 turns parallelism off. |
| `WithTrimmedPadding()` | Make an `Encoder` leave out the padding at the end of source symbols. The `Decoder` always accepts such symbols. |
| `WithBlockCache(n)` | Keep at most n source blocks ready in an `Encoder`. The default is 1 for `NewEncoderReaderAt` and no limit for `NewEncoder`. |

A `Decoder` decodes each block as soon as it has enough symbols.
`SetDeferredDecode(true)` waits for `Decode` instead, which decodes the
blocks in parallel. `go doc github.com/mstephenholl/graptor-q Option` lists
every option.

## Untrusted input

RaptorQ detects no corruption. One altered symbol makes the decoder return
wrong data without an error, so authenticate packets that cross an untrusted
network, for example with a MAC. `WithMaxMemory` limits each block, not the
object, so also check `TransferLength` in an untrusted OTI.

## Performance

On one core of an Intel Core Ultra 7 155U, graptor-q decodes 1.4–2.2× as
fast as the fastest of the other Go libraries measured, and 2.0–3.5× as fast
as cberner/raptorq, a Rust library. Encoding is 1.1–2.0× and 1.5–3.7× as
fast, respectively. [docs/performance.md](docs/performance.md) has the
figures, the method, and the SIMD kernel tiers.

## Correctness and compatibility

graptor-q's encoding symbols match cberner/raptorq 2.0.1 byte for byte, on
committed golden vectors for 49 objects and in live tests at every K'.
[docs/validation.md](docs/validation.md) lists the other checks.

xssnick/raptorq v1.5.2 deviates from RFC 6330 at 99 of the 477 K' values,
starting at K' = 49. At those block sizes its repair symbols differ from
those of graptor-q and cberner, and it does not interoperate with them.

## Development

`make test` runs the test suite.
[docs/development.md](docs/development.md) describes the repository layout
and the other make targets, and [ROADMAP.md](ROADMAP.md) lists planned work.

## Notes

RFC 6330 has intellectual property disclosures on file with the IETF,
including from Qualcomm. Review them before you deploy.
