# Builds rqoracle, the cberner/raptorq 2.0.1 oracle behind graptorq's golden
# vectors and interop tests, without a local Rust toolchain.
#
#   docker build -t graptorq-rqoracle tools/rqoracle
#   docker run --rm -i graptorq-rqoracle gen-vectors
#
# On Alpine the binary is linked statically against musl, so it also runs
# directly on any Linux host of the same architecture: `make oracle-docker`
# copies it out, which avoids starting a container for each of the many
# oracle calls in the interop tests.

FROM rust:1.98.1-alpine AS build
RUN apk add --no-cache musl-dev
WORKDIR /src

# Build the dependencies in their own layer, so that editing main.rs does
# not refetch and rebuild them.
COPY Cargo.toml Cargo.lock ./
RUN mkdir src && echo 'fn main() {}' > src/main.rs \
    && cargo build --release --locked \
    && rm -rf src

COPY src ./src
# COPY keeps the source's older mtime, so make sure cargo rebuilds.
RUN touch src/main.rs && cargo build --release --locked

FROM scratch
COPY --from=build /src/target/release/rqoracle /rqoracle
ENTRYPOINT ["/rqoracle"]
