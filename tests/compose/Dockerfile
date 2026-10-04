FROM golang:1.26

WORKDIR /workspace

COPY go.mod go.mod
COPY tests/compose/ tests/compose/

RUN CGO_ENABLED=0 go build -o /usr/local/bin/ezlb-compose-backend ./tests/compose/backend && \
    CGO_ENABLED=0 go build -o /usr/local/bin/ezlb-compose-verifier ./tests/compose/verifier
