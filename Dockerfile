FROM golang:1.26-bookworm AS builder

WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/recon ./cmd/recon

FROM golang:1.26-bookworm AS tools

WORKDIR /tmp/recon
COPY --from=builder /out/recon /usr/local/bin/recon
COPY resolvers.txt ./resolvers.txt
COPY wordlists ./wordlists

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates git make gcc libc6-dev \
    && rm -rf /var/lib/apt/lists/*

RUN git clone --depth 1 https://github.com/blechschmidt/massdns.git /tmp/massdns \
    && make -C /tmp/massdns \
    && mkdir -p /opt/recon-tools/bin \
    && install /tmp/massdns/bin/massdns /opt/recon-tools/bin/massdns

RUN recon install-tools --tools-dir /opt/recon-tools --include-optional=true

FROM debian:bookworm-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY --from=builder /out/recon /usr/local/bin/recon
COPY --from=tools /opt/recon-tools /opt/recon-tools
COPY resolvers.txt ./resolvers.txt
COPY wordlists ./wordlists

ENV PATH="/opt/recon-tools/bin:${PATH}"

ENTRYPOINT ["recon"]
