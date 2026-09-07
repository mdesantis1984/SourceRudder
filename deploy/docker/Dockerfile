FROM golang:1.26.6-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -o /bin/ia-buscar ./cmd/ia-buscar

FROM scratch

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=builder /bin/ia-buscar /usr/local/bin/ia-buscar

USER 65532:65532
EXPOSE 8080

ENTRYPOINT ["ia-buscar"]
CMD ["-transport", "http", "-http-addr", ":8080"]
