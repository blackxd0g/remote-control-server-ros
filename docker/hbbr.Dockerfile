FROM rust:1.98.0-slim-bookworm AS build
ARG TARGETARCH
ARG TARGETVARIANT
RUN apt-get update \
    && apt-get install -y --no-install-recommends musl-tools \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /src
COPY Cargo.toml Cargo.lock ./
COPY art-core/ art-core/
COPY art-hbbs/ art-hbbs/
COPY art-hbbr/ art-hbbr/
RUN case "$TARGETARCH/$TARGETVARIANT" in \
      amd64/) target=x86_64-unknown-linux-musl ;; \
      arm64/) target=aarch64-unknown-linux-musl ;; \
      *) echo "unsupported target: $TARGETARCH/$TARGETVARIANT" >&2; exit 1 ;; \
    esac \
    && rustup target add "$target" \
    && cargo build --locked --release --target "$target" -p art-hbbr \
    && cp "target/$target/release/art-hbbr" /out-art-hbbr
RUN mkdir -p /empty-data

FROM scratch
COPY --from=build /out-art-hbbr /art-hbbr
COPY --from=build --chown=65532:65532 /empty-data /data
USER 65532:65532
VOLUME ["/data"]
EXPOSE 21117/tcp 21119/tcp 21119/udp
ENTRYPOINT ["/art-hbbr"]
