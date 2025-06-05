# OpenHands Runtime Go

A Go implementation of the OpenHands runtime server.

## Building

```bash
go build -o openhands-runtime-go .
```

## Running

```bash
./openhands-runtime-go server
```

The server will start on port 8000 by default.

## Docker

### Building the Docker image

```bash
docker build -t openhands-runtime-go .
```

### Running with Docker

```bash
docker run -p 8000:8000 openhands-runtime-go
```

## Docker Hub

The Docker image is automatically built and published to GitHub Container Registry (GHCR) for both `linux/amd64` and `linux/arm64` architectures.

Pull the latest image:

```bash
docker pull ghcr.io/denysvitali/openhands-runtime-go:latest
```

## API Endpoints

- `GET /` - Server information
- `POST /execute` - Execute commands
- `POST /upload` - Upload files
- `GET /files` - List files
- Additional endpoints for file operations, IPython, and browser interactions

## Configuration

The server can be configured via environment variables or command-line flags. See `--help` for available options.
