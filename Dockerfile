# Build stage
FROM golang:1.24-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY api/ api/
COPY pkg/ pkg/
COPY cmd/ cmd/

ARG VERSION=0.1.0-dev
ARG GIT_COMMIT=unknown
ARG BUILD_DATE=unknown
ARG TARGETOS=linux
ARG TARGETARCH=amd64

RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
    -ldflags="-w -s -X main.version=${VERSION} -X main.gitCommit=${GIT_COMMIT} -X main.buildDate=${BUILD_DATE}" \
    -o /bin/ovn-topologyd ./cmd/ovn-topologyd

# Final runtime stage: distroless non-root
FROM gcr.io/distroless/static:nonroot

USER 65534:65534

COPY --from=builder /bin/ovn-topologyd /ovn-topologyd

EXPOSE 8080

ENTRYPOINT ["/ovn-topologyd"]
