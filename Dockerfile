# syntax=docker/dockerfile:1.7

# ---- build ----------------------------------------------------------------
FROM golang:1.27 AS build
WORKDIR /src
ENV CGO_ENABLED=0 GOFLAGS=-trimpath

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    go build -ldflags="-s -w -X main.version=${VERSION}" -o /out/api ./cmd/api && \
    go build -ldflags="-s -w" -o /out/migrate ./cmd/migrate && \
    mkdir -p /out/data/uploads

# ---- runtime --------------------------------------------------------------
# distroless/static: no shell, no package manager, CA certs + tzdata only.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/api /out/migrate /usr/local/bin/
COPY --from=build --chown=65532:65532 /out/data /data
ENV APP_ENV=prod HTTP_ADDR=:8080 UPLOAD_DIR=/data/uploads
USER 65532:65532
EXPOSE 8080
VOLUME ["/data/uploads"]
HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 CMD ["/usr/local/bin/api", "healthcheck"]
ENTRYPOINT ["/usr/local/bin/api"]
