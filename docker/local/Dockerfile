FROM golang:1.20-alpine AS builder

RUN apk add make git

WORKDIR /xgrchain

ADD go.mod go.sum ./
RUN go mod download

COPY . .

RUN make build

FROM alpine:latest AS runner

RUN apk --no-cache add ca-certificates jq

WORKDIR /xgrchain

COPY --from=builder /xgrchain/xgrchain ./
COPY ./docker/local/xgrchain.sh ./

# Expose json-rpc, libp2p and grpc ports
EXPOSE 8545 9632 1478 5001

ENTRYPOINT ["./xgrchain.sh"]
