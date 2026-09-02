FROM --platform=$BUILDPLATFORM node:24-alpine AS web-build
WORKDIR /src/art-web
RUN corepack enable
COPY VERSION /src/VERSION
COPY art-web/package.json art-web/pnpm-lock.yaml art-web/pnpm-workspace.yaml ./
RUN pnpm install --frozen-lockfile
COPY art-web/ ./
RUN pnpm build

FROM --platform=$BUILDPLATFORM golang:1.26.7-alpine3.24 AS build
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
RUN mkdir -p /empty-data

FROM scratch
COPY --from=build /out/art-api /art-api
COPY --from=build --chown=65532:65532 /empty-data /data
USER 65532:65532
VOLUME ["/data"]
EXPOSE 21114
ENTRYPOINT ["/art-api"]
