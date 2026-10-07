FROM golang:1.22-alpine AS builder
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/dodo-payments ./cmd/api

FROM alpine:3.19
WORKDIR /app
COPY --from=builder /out/dodo-payments /app/dodo-payments
COPY --from=builder /src/migrations /app/migrations
EXPOSE 8080
CMD ["/app/dodo-payments"]
