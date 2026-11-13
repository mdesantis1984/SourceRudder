FROM docker.io/library/golang:1.27.4-alpine@sha256:0a5f7fe796599ff487605987432e7619ae45beeb2fba4c2ce0b907879932aebf AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -o /bin/sourcerudder ./cmd/sourcerudder
RUN CGO_ENABLED=0 GOOS=linux go build -o /bin/sourcerudder-healthcheck ./cmd/healthcheck

FROM scratch

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=builder /bin/sourcerudder /usr/local/bin/sourcerudder
COPY --from=builder /bin/sourcerudder-healthcheck /usr/local/bin/sourcerudder-healthcheck

USER 65532:65532
EXPOSE 8080
HEALTHCHECK --interval=5s --timeout=3s --start-period=10s --retries=12 CMD ["/usr/local/bin/sourcerudder-healthcheck"]

ENTRYPOINT ["sourcerudder"]
CMD ["-transport", "http", "-http-addr", ":8080"]
