FROM golang:1.21-alpine AS builder
RUN apk add --no-cache git
WORKDIR /app
COPY go.mod ./
COPY main.go .
RUN go mod tidy && CGO_ENABLED=0 go build -ldflags="-s -w" -o relay main.go

FROM alpine:latest
WORKDIR /data
COPY --from=builder /app/relay /usr/local/bin/relay
EXPOSE 8888 9999
ENTRYPOINT ["relay"]
CMD ["-mode", "master"]