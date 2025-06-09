FROM golang:1.24-alpine AS builder

WORKDIR /app

RUN apk add --no-cache git

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
    -a -installsuffix cgo \
    -o openhands-runtime-go .

FROM alpine:latest

RUN apk --no-cache add \
    ca-certificates \
    bash \
    wget \
    go \
    golangci-lint \
    python3 \
    ipython \
    py3-pip \
    py3-setuptools \
    py3-wheel \
    py3-numpy \
    py3-pandas \
    py3-matplotlib \
    py3-seaborn

RUN addgroup -g 1001 -S appgroup && \
    adduser -u 1001 -S appuser -G appgroup

WORKDIR /root/

COPY --from=builder /app/openhands-runtime-go .
RUN chown appuser:appgroup openhands-runtime-go

USER appuser

EXPOSE 8000

CMD ["./openhands-runtime-go", "server"]
