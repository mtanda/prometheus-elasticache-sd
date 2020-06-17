FROM golang:1.14 AS builder

WORKDIR /go/src/github.com/rrreeeyyy/prometheus-elasticache-sd

COPY . .

RUN env GOARCH=amd64 GOOS=linux CGO_ENABLED=0 go build -o /prometheus-elasticache-sd .

FROM alpine:edge
RUN apk add --update --no-cache ca-certificates
COPY --from=builder /prometheus-elasticache-sd /prometheus-elasticache-sd
USER nobody
ENTRYPOINT ["/prometheus-elasticache-sd"]
