FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY main.go .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o relay main.go

FROM alpine:latest
WORKDIR /data
COPY --from=builder /app/relay /usr/local/bin/relay
EXPOSE 8888 9999
ENTRYPOINT ["relay"]
CMD ["-mode", "master"]
