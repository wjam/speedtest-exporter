FROM alpine:3.24 AS dependency

WORKDIR /

RUN wget -O /tmp/speedtest.tgz "https://install.speedtest.net/app/cli/ookla-speedtest-1.2.0-linux-$(apk info --print-arch).tgz" && tar xvfz /tmp/speedtest.tgz speedtest

FROM golang:1.26-alpine3.24 AS builder

WORKDIR /src
COPY go.mod .
COPY go.sum .

RUN go mod download

COPY . .

RUN CGO_ENABLED=0 go build -o speedtest-exporter -ldflags "-w -s" -trimpath

FROM gcr.io/distroless/static-debian13
EXPOSE 9876
WORKDIR /usr/local/bin

COPY --from=builder /src/speedtest-exporter .
COPY --from=dependency /speedtest .

ENTRYPOINT ["/usr/local/bin/speedtest-exporter"]
