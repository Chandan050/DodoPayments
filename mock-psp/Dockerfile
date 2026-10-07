FROM golang:1.22-alpine AS builder
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/mock-psp .

FROM alpine:3.19
WORKDIR /app
COPY --from=builder /out/mock-psp /app/mock-psp
EXPOSE 8081
CMD ["/app/mock-psp"]
