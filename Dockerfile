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
    bazel \
    curl \
    busybox \
    wget \
    git \
    go \
    golangci-lint \
    nix \
    jq \
    rg \
    perl \
    python3 \
    ipython \
    py3-pip \
    py3-setuptools \
    py3-wheel \
    py3-numpy \
    py3-pandas \
    py3-matplotlib \
    py3-seaborn \
    make \
    cmake \
    build-base \
    protobuf \
    protobuf-dev \
    protoc \
    gawk \
    sed \
    findutils \
    coreutils \
    tar \
    gzip \
    unzip \
    tree \
    vim \
    nano \
    kubectl \
    helm

# Install essential Go tools for monorepo development
RUN go install github.com/bazelbuild/buildtools/buildifier@latest && \
    go install github.com/bazelbuild/buildtools/buildozer@latest && \
    go install github.com/bazelbuild/bazel-gazelle/cmd/gazelle@latest && \
    go install golang.org/x/tools/cmd/goimports@latest && \
    go install golang.org/x/tools/cmd/godoc@latest && \
    go install golang.org/x/tools/gopls@latest

# Ensure Go bin is in PATH for the openhands user
ENV PATH="/home/openhands/go/bin:${PATH}"

RUN addgroup -g 1001 -S openhands && \
    adduser -u 1001 -S openhands -G openhands && \
    mkdir -p /app /openhands/code /home/openhands/go/bin && \
    chown -R openhands:openhands /app /openhands/code /home/openhands

WORKDIR /openhands/code

COPY --from=builder /app/openhands-runtime-go /app/

# Set up Go workspace for the openhands user
USER openhands
RUN go env -w GOPATH=/home/openhands/go && \
    go env -w GOBIN=/home/openhands/go/bin

EXPOSE 8000

CMD ["/app/openhands-runtime-go", "server"]
