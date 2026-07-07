FROM golang:1.21-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /diffflow-server ./cmd/diffflow-server

FROM scratch
COPY --from=builder /diffflow-server /diffflow-server
COPY configs/default.toml /configs/default.toml
EXPOSE 8090
ENTRYPOINT ["/diffflow-server"]
