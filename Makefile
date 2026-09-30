VECTORS := testdata/vectors/cberner-2.0.1/vectors.jsonl.gz

# The cberner/raptorq oracle is built with the local Rust toolchain by
# default, or in Docker with ORACLE=docker (for example
# `make interop ORACLE=docker`), which needs no Rust toolchain.
ORACLE_IMAGE := graptorq-rqoracle
ifeq ($(ORACLE),docker)
RQORACLE      := $(CURDIR)/tools/rqoracle/target/docker/rqoracle
ORACLE_TARGET := oracle-docker
else
RQORACLE      := $(CURDIR)/tools/rqoracle/target/release/rqoracle
ORACLE_TARGET := oracle
endif

.PHONY: all test test-short test-purego test-race test-cross vet generate check-generate \
        test-tiers oracle oracle-docker oracle-vectors interop bench bench-compare stat-long fuzz

all: vet test

test:
	go test ./...

test-short:
	go test -short ./...

# The portable Go kernels only.
test-purego:
	go test -tags purego ./...

test-race:
	go test -race -short ./...

# The whole suite with each amd64 kernel tier forced (TestEnvTier reports
# tiers this CPU does not support as skipped).
TIERS ?= generic ssse3 avx2 gfni
test-tiers:
	for t in $(TIERS); do echo "== tier $$t"; GRAPTORQ_GF256=$$t go test -short ./... || exit 1; done

# arm64 (NEON kernels) and s390x (big-endian) under qemu-user, plus 32-bit x86.
test-cross:
	GOARCH=arm64 go test -short -exec qemu-aarch64-static ./...
	GOARCH=s390x go test -short -exec qemu-s390x-static ./...
	GOARCH=386 go test -short ./...

vet:
	go vet ./...
	GOARCH=arm64 go vet ./...
	go vet -tags purego ./...
	cd interop && go vet ./...

# Regenerate internal/rfc/tables_gen.go from the RFC text.
generate:
	cd internal/rfc && go generate

check-generate: generate
	git diff --exit-code internal/rfc/tables_gen.go

# cberner/raptorq 2.0.1 oracle, built with the local Rust toolchain.
oracle:
	cd tools/rqoracle && cargo build --release --locked

# The same oracle built in Docker. The image's binary is statically linked,
# so it is copied out and run directly: the interop tests call it hundreds of
# times, which would be slow with a container per call.
oracle-docker:
	docker build -t $(ORACLE_IMAGE) tools/rqoracle
	mkdir -p $(dir $(RQORACLE))
	id=$$(docker create $(ORACLE_IMAGE)) && \
		{ docker cp -q $$id:/rqoracle $(RQORACLE); status=$$?; docker rm -f $$id >/dev/null; exit $$status; }

oracle-vectors: $(ORACLE_TARGET)
	$(RQORACLE) gen-vectors | gzip -9 -n > $(VECTORS)

# Differential tests against xssnick/raptorq and live tests against cberner.
interop: $(ORACLE_TARGET)
	cd interop && RQORACLE=$(RQORACLE) go test ./...

bench:
	go test -run xxx -bench . ./internal/gf256/ ./internal/solver/

# Single-core comparison with xssnick (Go) and cberner (Rust) on CPU $(CPU),
# as reported in the README (median of the 5 runs of each benchmark). For
# cberner, prefer the native build: the Docker one uses musl's allocator.
CPU ?= 0
bench-compare: $(ORACLE_TARGET)
	cd interop && taskset -c $(CPU) go test -cpu 1 -run xxx -bench 'Cmp/lib=(graptorq|xssnick)/' -benchtime 1s -count 5 .
	taskset -c $(CPU) $(RQORACLE) bench

# RFC 6330 Section 5.8 recovery properties with 1.4 million trials.
stat-long:
	GRAPTORQ_LONG=1 go test -timeout 3h -run RecoveryProperties -v ./internal/solver/

FUZZTIME ?= 30s
fuzz:
	go test -run xxx -fuzz '^FuzzParseOTI$$' -fuzztime $(FUZZTIME) .
	go test -run xxx -fuzz '^FuzzDecoderPackets$$' -fuzztime $(FUZZTIME) .
	go test -run xxx -fuzz '^FuzzBlockRoundTrip$$' -fuzztime $(FUZZTIME) .
	go test -run xxx -fuzz '^FuzzMulAdd$$' -fuzztime $(FUZZTIME) ./internal/gf256/
