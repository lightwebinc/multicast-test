# The overlay host and header anchor of the ITCVE-2 joined configuration,
# built from this repository's tpnil/cmd sources. Build context: the
# repository root.
FROM golang:1.27-alpine@sha256:70bd7ddea8b6cbe5d6943cde279d6b67feb10cc924fb9100d1d8757c1575b280 AS builder
WORKDIR /src
COPY go.mod go.sum ./
COPY tpnil/cmd/overlay-host tpnil/cmd/overlay-host
COPY tpnil/cmd/header-anchor tpnil/cmd/header-anchor
RUN set -eux; mkdir -p /out; \
    CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags "-s -w" -o /out/overlay-host ./tpnil/cmd/overlay-host; \
    CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags "-s -w" -o /out/header-anchor ./tpnil/cmd/header-anchor

FROM gcr.io/distroless/static:nonroot@sha256:8ce6dd87e9823c5b48dcfb2c8604714ccb351ce59607327bcc4957448da279b5
USER nonroot:nonroot
COPY --from=builder /out/ /usr/local/bin/
