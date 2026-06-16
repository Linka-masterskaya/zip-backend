FROM golang:1.26-alpine AS builder

WORKDIR /app

COPY go.mod ./
COPY go.sum ./

RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /zip-backend ./cmd/server

FROM alpine:latest

WORKDIR /app

COPY --from=builder /zip-backend ./zip-backend

COPY --from=builder /app/config ./config

EXPOSE 8080

CMD ["./zip-backend"]