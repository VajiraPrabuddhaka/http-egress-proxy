FROM golang:1.24-alpine AS builder

WORKDIR /app
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o http-egress-proxy .

FROM gcr.io/distroless/static:nonroot
COPY --from=builder /app/http-egress-proxy /http-egress-proxy
USER nonroot:nonroot
ENTRYPOINT ["/http-egress-proxy"]