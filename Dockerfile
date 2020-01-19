# from https://codefresh.io/docs/docs/learn-by-example/golang/golang-hello-world/
# ==== BUILDING ====
FROM golang:1-alpine AS build_base

RUN apk add --no-cache git
WORKDIR /tmp/mqtt-prometheus-exporter

COPY go.mod .
COPY go.sum .

RUN go mod download

COPY . .

RUN go build -o ./out/mqtt-prometheus-exporter .

# ==== RUN ====
FROM alpine:3

COPY --from=build_base /tmp/mqtt-prometheus-exporter/out/mqtt-prometheus-exporter /app/mqtt-prometheus-exporter/mqtt-prometheus-exporter

WORKDIR /app/mqtt-prometheus-exporter/
VOLUME [ "/app/mqtt-prometheus-exporter/config.yml" ]
ENTRYPOINT ["/app/mqtt-prometheus-exporter/mqtt-prometheus-exporter"]
