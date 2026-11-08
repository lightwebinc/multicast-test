# The overlay host and header anchor of the ITCVE-2 joined configuration,
# built from this repository's tpnil/cmd sources. Build context: the
# repository root.
FROM golang:1.27-alpine@sha256:fa3d596898d9f126af7c01d14ee7597bab3f39cccf392ef36b69fe77cd681f18 AS builder
WORKDIR /src
COPY go.mod go.sum ./
COPY tpnil/cmd/overlay-host tpnil/cmd/overlay-host
COPY tpnil/cmd/header-anchor tpnil/cmd/header-anchor
RUN set -eux; mkdir -p /out; \
    CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags "-s -w" -o /out/overlay-host ./tpnil/cmd/overlay-host; \
    CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags "-s -w" -o /out/header-anchor ./tpnil/cmd/header-anchor

FROM gcr.io/distroless/static:nonroot@sha256:25ac8f0c3b2334a9eb05452266bd4697864a8c8ef0ef71e5e6480d7f92868329
USER nonroot:nonroot
COPY --from=builder /out/ /usr/local/bin/
