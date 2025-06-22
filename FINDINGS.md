
# Findings Report

## Project: openhands-runtime-go

### Structure Overview:
*   `cmd/`: Contains `root.go` and `server.go`, likely for CLI and server entry points.
*   `internal/`: Contains `models/`, suggesting internal data structures/business logic.
*   `pkg/`: Contains `config/`, `executor/`, `mcp/`, `server/`, and `telemetry/`, indicating modular components for configuration, command execution, server logic, and observability.

### Initial Assessment of Functionality:
The project structure suggests a server application with command execution capabilities (`executor/`).

## Project: All-Hands-AI/OpenHands (Reference)

### Core Functionality:
OpenHands agents are designed to "modify code, run commands, browse the web, call APIs." This aligns with the `executor/` concept in the Go project.

### Architecture Insights from `README.md`:
*   **Sandbox/Runtime Container:** OpenHands utilizes a separate Docker container for its runtime/sandbox, indicated by `SANDBOX_RUNTIME_CONTAINER_IMAGE`. This implies a client-server or host-agent model where OpenHands orchestrates code execution within isolated environments.
*   **Docker Socket Mount:** The `-v /var/run/docker.sock:/var/run/docker.sock` mount suggests that OpenHands directly interacts with the Docker daemon to manage these sandbox containers.
*   **Persistent Data:** Uses `~/.openhands` for persistent data.
*   **Default Port:** Runs on port 3000.

### Missing Features in Go Implementation (based on problem description):
*   **Advanced Editor Support:** The Go project currently lacks an advanced editor.
*   **Persistent Bash Session / Tmux:** The Go project does not appear to support persistent bash sessions or tmux, which are likely features of the reference OpenHands project for interactive development.

## Comparison Summary:

The `openhands-runtime-go` project appears to be a reimplementation of the core runtime functionality of `All-Hands-AI/OpenHands`. The key difference identified so far is the lack of advanced editor support and persistent bash/tmux sessions in the Go version. The Go project's `executor` package will be critical to compare against how OpenHands handles command execution and sandboxing. The Go project will need to replicate the sandbox orchestration capabilities of the reference OpenHands project.

## Next Steps:
1.  Deep dive into the `executor/` package in `openhands-runtime-go` to understand its current capabilities.
2.  Investigate how `All-Hands-AI/OpenHands` implements its sandbox, persistent sessions, and editor integration (likely through its API or specific runtime components).
3.  Define the API contract that needs to be maintained.
4.  Formulate a detailed roadmap for bridging the functional gaps and ensuring performance parity.
