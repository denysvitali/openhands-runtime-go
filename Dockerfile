# Build stage
FROM golang:1.24-alpine AS builder

# Set working directory
WORKDIR /app

# Install git (needed for go mod download)
RUN apk add --no-cache git

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o openhands-runtime-go .

# Final stage
FROM alpine:latest

# Install ca-certificates for HTTPS requests and Python with IPython
RUN apk --no-cache add ca-certificates bash wget python3 py3-pip go golangci-lint && \
    python3 -m venv /opt/venv && \
    . /opt/venv/bin/activate && \
    pip install --no-cache-dir ipython jupyter matplotlib numpy pandas seaborn plotly

# Add virtual environment to PATH
ENV PATH="/opt/venv/bin:$PATH"

# Create a non-root user
RUN addgroup -g 1001 -S appgroup && \
    adduser -u 1001 -S appuser -G appgroup

WORKDIR /root/

# Copy the binary from builder stage
COPY --from=builder /app/openhands-runtime-go .

# Change ownership to non-root user
RUN chown appuser:appgroup openhands-runtime-go

# Switch to non-root user
USER appuser

# Expose port
EXPOSE 8000

# Command to run
CMD ["./openhands-runtime-go", "server"]
