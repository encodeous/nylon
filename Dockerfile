FROM golang:1.25.4 AS builder
WORKDIR /src

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags "-s -w" -o /nylon .

FROM scratch

# Copy binary from builder
COPY --from=builder /nylon /usr/local/bin/nylon

WORKDIR /app/config

ENTRYPOINT ["/usr/local/bin/nylon", "run", "-v"]

FROM ubuntu:latest AS debug

RUN apt-get update && apt-get install -y \
    iputils-ping \
    iperf3 \
    curl \
    iproute2 \
    net-tools \
    tcpdump \
    dnsutils \
    vim \
    && rm -rf /var/lib/apt/lists/*

COPY --from=builder /nylon /usr/local/bin/nylon

WORKDIR /app/config

ENTRYPOINT ["/usr/local/bin/nylon", "run", "-v"]
