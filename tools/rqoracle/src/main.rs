//! rqoracle: an RFC 6330 oracle for graptorq built on cberner/raptorq 2.0.1.
//!
//! Subcommands:
//!   gen-vectors                         JSON lines of golden vectors on stdout
//!   encode <oti-hex> <seed> <repair>    all source packets plus <repair> repair
//!                                       packets per block of pattern(F, seed),
//!                                       as u32-BE length-prefixed packets
//!   decode <oti-hex>                    length-prefixed packets on stdin; the
//!                                       decoded object on stdout (exit 2 if the
//!                                       packets are not enough)
//!   bench                               single-threaded encode/decode throughput
//!                                       in Go benchmark format (the workloads
//!                                       of interop's BenchmarkCmp)
//!
//! Packets are the RFC 6330 encoding packets: a 4-byte FEC Payload ID followed
//! by one symbol. The object data is pattern(F, seed), shared with the Go tests
//! (internal/testutil.PatternData).

use raptorq::{
    Decoder, Encoder, EncodingPacket, ObjectTransmissionInformation, SourceBlockDecoder,
    SourceBlockEncoder,
};
use std::hint::black_box;
use std::time::Instant;
use std::io::{self, Read, Write};
use std::process::exit;

fn pattern(n: usize, seed: u64) -> Vec<u8> {
    (0..n)
        .map(|i| ((i as u64).wrapping_add(seed).wrapping_mul(0x9E37_79B9_7F4A_7C15) >> 56) as u8)
        .collect()
}

fn hex(b: &[u8]) -> String {
    b.iter().map(|x| format!("{:02x}", x)).collect()
}

fn unhex(s: &str) -> Vec<u8> {
    (0..s.len())
        .step_by(2)
        .map(|i| u8::from_str_radix(&s[i..i + 2], 16).expect("bad hex"))
        .collect()
}

fn parse_oti(s: &str) -> ObjectTransmissionInformation {
    let b = unhex(s);
    let arr: [u8; 12] = b.as_slice().try_into().expect("OTI must be 12 bytes");
    let o = ObjectTransmissionInformation::deserialize(&arr);
    // Re-create through new() so that its validity assertions run.
    ObjectTransmissionInformation::new(
        o.transfer_length(),
        o.symbol_size(),
        o.source_blocks(),
        o.sub_blocks(),
        o.symbol_alignment(),
    )
}

/// The symbol with the given ESI from a block encoder, checking the Payload ID
/// that cberner reports.
fn symbol(block: &raptorq::SourceBlockEncoder, sbn: u8, k: u32, esi: u32) -> Vec<u8> {
    let pkt = if esi < k {
        block.source_packets().swap_remove(esi as usize)
    } else {
        block.repair_packets(esi - k, 1).pop().unwrap()
    };
    assert_eq!(pkt.payload_id().source_block_number(), sbn);
    assert_eq!(pkt.payload_id().encoding_symbol_id(), esi);
    pkt.data().to_vec()
}

struct Case {
    name: &'static str,
    f: u64,
    t: u16,
    z: u8,
    n: u16,
    al: u8,
    seed: u64,
    decode: bool,
}

fn emit_case(out: &mut impl Write, c: &Case) {
    let oti = ObjectTransmissionInformation::new(c.f, c.t, c.z, c.n, c.al);
    let data = pattern(c.f as usize, c.seed);
    let enc = Encoder::new(&data, oti);
    let mut syms: Vec<String> = Vec::new();
    for (sbn, block) in enc.get_block_encoders().iter().enumerate() {
        let sbn = sbn as u8;
        let k = block.source_packets().len() as u32;
        let esis: Vec<u32> = if c.decode {
            // Every third source symbol is lost; it is replaced by repair
            // symbols, with two extra for good measure.
            let mut v: Vec<u32> = (0..k).filter(|i| i % 3 != 1).collect();
            let lost = k - v.len() as u32;
            v.extend(k + 3..k + 3 + lost + 2);
            v
        } else {
            let mut v = vec![0, k - 1, k, k + 1, k + 17, k + 1000, (1 << 24) - 1];
            v.sort();
            v.dedup();
            v
        };
        let source = block.source_packets();
        for esi in esis {
            let data = if esi < k {
                source[esi as usize].data().to_vec()
            } else {
                symbol(block, sbn, k, esi)
            };
            syms.push(format!("{{\"sbn\":{},\"esi\":{},\"data\":\"{}\"}}", sbn, esi, hex(&data)));
        }
    }
    writeln!(
        out,
        "{{\"name\":\"{}\",\"oti\":\"{}\",\"seed\":{},\"decode\":{},\"symbols\":[{}]}}",
        c.name,
        hex(&oti.serialize()),
        c.seed,
        c.decode,
        syms.join(",")
    )
    .unwrap();
}

fn gen_vectors() {
    let stdout = io::stdout();
    let mut out = io::BufWriter::new(stdout.lock());
    let mut cases: Vec<Case> = Vec::new();
    let mut seed = 1;
    let mut add = |name, f, t, z, n, al, decode| {
        cases.push(Case { name, f, t, z, n, al, seed, decode });
        seed += 1;
    };

    // Single source blocks across Table 2 transitions (K != K' included),
    // with a partial last symbol where T allows it.
    for &k in &[1u64, 2, 9, 10, 11, 12, 18, 19, 48, 49, 50, 101, 1021, 1032, 4096, 10000, 56403] {
        add("block", k * 8, 8, 1, 1, 1, false);
        if k <= 4096 {
            add("block-partial", k * 16 - 5, 16, 1, 1, 1, false);
        }
    }
    for &k in &[10u64, 100, 1000] {
        add("block-t1280", k * 1280 - 100, 1280, 1, 1, 4, false);
    }
    add("tiny", 1, 4, 1, 1, 4, false);
    add("t1", 777, 1, 1, 1, 1, false);

    // Several source blocks, sub-blocks with TL != TS, several alignments.
    add("z2", 20_000, 64, 2, 1, 8, false);
    add("z7-n3", 50_003, 96, 7, 3, 4, false); // T/Al = 24 -> (8,8,3,0)
    add("z3-n4", 30_001, 24, 3, 4, 4, false); // T/Al = 6 -> (2,1,2,2)
    add("n5-al1", 9_999, 23, 1, 5, 1, false); // (5,4,3,2)
    add("n8-al8", 65_536, 136, 2, 8, 8, false); // T/Al = 17 -> (3,2,1,7)
    add("z255", 255 * 3 * 8 - 5, 8, 255, 1, 8, false);
    add("z255-n2", 255 * 5 * 16 - 7, 16, 255, 2, 4, false);
    add("n-max", 4_000, 32, 1, 32, 1, false); // one-byte sub-symbols

    // Decode direction: cberner packets that graptorq must decode.
    add("decode-block", 5_000, 16, 1, 1, 4, true);
    add("decode-z3", 12_345, 32, 3, 1, 4, true);
    add("decode-n3", 7_777, 40, 2, 3, 4, true); // T/Al = 10 -> (4,3,1,2)
    add("decode-z5-n4", 20_011, 24, 5, 4, 2, true);

    for c in &cases {
        emit_case(&mut out, c);
    }

    // RFC 6330 Section 4.3 as implemented by cberner (Al = 8, SS = 8,
    // WS = 10 MiB).
    for &mtu in &[64u16, 1280, 1500, 65535] {
        for &f in &[1u64, 1000, 1_000_000, 100_000_000, 10_000_000_000, 500_000_000_000] {
            let oti = ObjectTransmissionInformation::with_defaults(f, mtu);
            let kt = (f + oti.symbol_size() as u64 - 1) / oti.symbol_size() as u64;
            if kt > 255 * 56403 {
                continue; // cberner silently truncates Z to u8
            }
            writeln!(
                out,
                "{{\"name\":\"derive\",\"f\":{},\"mtu\":{},\"oti\":\"{}\"}}",
                f,
                mtu,
                hex(&oti.serialize())
            )
            .unwrap();
        }
    }
}

fn encode(oti_hex: &str, seed: u64, repair: u32) {
    let oti = parse_oti(oti_hex);
    let data = pattern(oti.transfer_length() as usize, seed);
    let enc = Encoder::new(&data, oti);
    let stdout = io::stdout();
    let mut out = io::BufWriter::new(stdout.lock());
    for pkt in enc.get_encoded_packets(repair) {
        let b = pkt.serialize();
        out.write_all(&(b.len() as u32).to_be_bytes()).unwrap();
        out.write_all(&b).unwrap();
    }
}

fn decode(oti_hex: &str) {
    let oti = parse_oti(oti_hex);
    let mut input = Vec::new();
    io::stdin().read_to_end(&mut input).unwrap();
    let mut dec = Decoder::new(oti);
    let mut pos = 0;
    while pos + 4 <= input.len() {
        let n = u32::from_be_bytes(input[pos..pos + 4].try_into().unwrap()) as usize;
        pos += 4;
        let pkt = EncodingPacket::deserialize(&input[pos..pos + n]);
        pos += n;
        if let Some(data) = dec.decode(pkt) {
            io::stdout().write_all(&data).unwrap();
            return;
        }
    }
    eprintln!("rqoracle: not enough packets to decode");
    exit(2);
}

/// Mirrors interop's BenchmarkCmp: encode = build the block encoder and one
/// repair symbol; decode = 10% of the source symbols lost (ESI % 10 == 3),
/// replaced by as many repair symbols plus two.
fn bench() {
    for &(k, t) in &[(100usize, 1280u16), (1000, 1280), (10000, 1280), (50000, 256)] {
        let data = pattern(k * t as usize, 1);
        let oti = ObjectTransmissionInformation::new(data.len() as u64, t, 1, 1, 1);
        let iters = (2_000_000_000 / data.len()).clamp(3, 200) as u32;
        let report = |op: &str, elapsed: std::time::Duration| {
            let ns = elapsed.as_nanos() as f64 / iters as f64;
            println!(
                "BenchmarkCmp/lib=cberner/op={}/K={}/T={}-1\t{}\t{:.0} ns/op\t{:.2} MB/s",
                op, k, t, iters, ns, data.len() as f64 / ns * 1e3
            );
        };

        let start = Instant::now();
        for _ in 0..iters {
            let enc = SourceBlockEncoder::new(0, &oti, &data);
            black_box(enc.repair_packets(0, 1));
        }
        report("encode", start.elapsed());

        let enc = SourceBlockEncoder::new(0, &oti, &data);
        let source = enc.source_packets();
        let mut packets: Vec<EncodingPacket> = Vec::new();
        let mut lost = 0;
        for (i, p) in source.into_iter().enumerate() {
            if i % 10 == 3 {
                lost += 1;
            } else {
                packets.push(p);
            }
        }
        packets.extend(enc.repair_packets(0, lost + 2));
        let start = Instant::now();
        for _ in 0..iters {
            let mut dec = SourceBlockDecoder::new(0, &oti, data.len() as u64);
            let out = dec.decode(packets.clone()).expect("decode failed");
            black_box(out);
        }
        report("decode", start.elapsed());
    }
}

fn main() {
    let args: Vec<String> = std::env::args().collect();
    match args.get(1).map(String::as_str) {
        Some("gen-vectors") => gen_vectors(),
        Some("encode") if args.len() == 5 => encode(&args[2], args[3].parse().unwrap(), args[4].parse().unwrap()),
        Some("decode") if args.len() == 3 => decode(&args[2]),
        Some("bench") => bench(),
        _ => {
            eprintln!("usage: rqoracle gen-vectors | encode <oti-hex> <seed> <repair> | decode <oti-hex> | bench");
            exit(1);
        }
    }
}
