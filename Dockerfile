FROM golang:1.25 AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o streamer /app/cmd/streamer.go

FROM alpine:latest
COPY --from=builder /app/streamer /app/streamer
WORKDIR /app
ENTRYPOINT ["./streamer"]
