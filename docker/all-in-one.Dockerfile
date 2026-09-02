FROM node:24-alpine AS web-build
WORKDIR /src/art-web
RUN corepack enable
COPY VERSION /src/VERSION
COPY art-web/package.json art-web/pnpm-lock.yaml art-web/pnpm-workspace.yaml ./
RUN pnpm install --frozen-lockfile
COPY art-web/ ./
RUN pnpm build

FROM golang:1.26.7-alpine3.24 AS go-build
ARG TARGETOS=linux
ARG TARGETARCH=amd64
WORKDIR /src
COPY art-api/go.mod art-api/go.sum ./
RUN go mod download
COPY VERSION /src/VERSION
COPY art-api/ ./
COPY --from=web-build /src/art-api/internal/webui/dist ./internal/webui/dist
RUN test -z "$(gofmt -l .)" && go test ./... && go vet ./...
RUN BUILD_VERSION="$(cat /src/VERSION)" && CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w -buildid= -X github.com/art-rustdesk/platform/art-api/internal/config.BuildVersion=${BUILD_VERSION}" -o /out/art-api ./cmd/art-api

FROM rust:1.98.0-alpine3.23 AS rust-build
ARG TARGETARCH=amd64
RUN test "$TARGETARCH" = "amd64" && apk add --no-cache musl-dev
RUN rustup target add x86_64-unknown-linux-musl
RUN rustup component add rustfmt clippy
WORKDIR /src
COPY Cargo.toml Cargo.lock ./
COPY art-core/ art-core/
COPY art-hbbs/ art-hbbs/
COPY art-hbbr/ art-hbbr/
RUN cargo fmt --all -- --check \
    && cargo clippy --locked --workspace --all-targets -- -D warnings \
    && cargo test --locked --workspace
RUN cargo build --locked --release --target x86_64-unknown-linux-musl -p art-hbbs -p art-hbbr

FROM alpine:3.23.5
RUN addgroup -S -g 65532 art && adduser -S -D -H -u 65532 -G art art \
    && mkdir -p /data && chown 65532:65532 /data
COPY --from=go-build /out/art-api /usr/local/bin/art-api
COPY --from=rust-build /src/target/x86_64-unknown-linux-musl/release/art-hbbs /usr/local/bin/art-hbbs
COPY --from=rust-build /src/target/x86_64-unknown-linux-musl/release/art-hbbr /usr/local/bin/art-hbbr
COPY docker/all-in-one-entrypoint.sh /usr/local/bin/art-entrypoint
RUN chmod 755 /usr/local/bin/art-entrypoint
USER 65532:65532
VOLUME ["/data"]
EXPOSE 21114/tcp 21115/tcp 21116/tcp 21116/udp 21117/tcp 21118/tcp 21119/tcp 21119/udp
ENTRYPOINT ["/usr/local/bin/art-entrypoint"]
