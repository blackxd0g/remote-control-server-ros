FROM --platform=$BUILDPLATFORM rust:1.98.0-alpine3.23 AS build
ARG TARGETARCH=amd64
RUN test "$TARGETARCH" = "amd64" && apk add --no-cache musl-dev
RUN rustup target add x86_64-unknown-linux-musl
WORKDIR /src
COPY Cargo.toml Cargo.lock ./
COPY art-core/ art-core/
COPY art-hbbs/ art-hbbs/
COPY art-hbbr/ art-hbbr/
RUN cargo build --locked --release --target x86_64-unknown-linux-musl -p art-hbbr
RUN mkdir -p /empty-data

FROM scratch
COPY --from=build /src/target/x86_64-unknown-linux-musl/release/art-hbbr /art-hbbr
COPY --from=build --chown=65532:65532 /empty-data /data
USER 65532:65532
VOLUME ["/data"]
EXPOSE 21117/tcp 21119/tcp 21119/udp
ENTRYPOINT ["/art-hbbr"]
