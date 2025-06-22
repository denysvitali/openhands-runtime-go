

# Project Roadmap: openhands-runtime-go Alignment

## Goal:
Align `openhands-runtime-go` with `All-Hands-AI/OpenHands` in terms of core functionality and API, focusing on performance, while addressing missing features like advanced editor support and persistent bash sessions/tmux.

## Phases:

### Phase 1: Deep Dive and API Definition (Current)
*   **Objective:** Understand existing implementations and define the target API.
*   **Tasks:**
    *   [x] Initial exploration of `openhands-runtime-go` project structure.
    *   [x] Initial exploration of `All-Hands-AI/OpenHands` `README.md` for high-level understanding.
    *   [x] Detailed analysis of `openhands-runtime-go`'s `pkg/executor` to understand current command execution.
    *   [x] Investigate `All-Hands-AI/OpenHands`'s sandbox implementation (e.g., how it manages containers, persistent sessions, and communication). This will likely involve looking at their codebase.
    *   [ ] Define the API contract that `openhands-runtime-go` must adhere to, based on `All-Hands-AI/OpenHands`'s behavior, specifically for command execution and interactive sessions.
    *   [ ] Identify specific functionalities within `All-Hands-AI/OpenHands` that need to be replicated or adapted in `openhands-runtime-go`.

### Phase 2: Core Functionality Alignment (Persistent Sessions & Sandboxing)
*   **Objective:** Implement persistent bash sessions (via tmux) and robust container management to match `All-Hands-AI/OpenHands`.
*   **Tasks:**
    *   [ ] **Container Orchestration:** Implement Docker client integration in `pkg/executor` to:
        *   Pull/build the OpenHands runtime image (or a compatible one).
        *   Create and start sandbox containers with appropriate volume mounts (for working directory) and port mappings (for VSCode).
        *   Manage container lifecycle (stop, remove).
    *   [ ] **Persistent Bash Session (Tmux):**
        *   Within the sandbox container, ensure `tmux` is installed and a session is started.
        *   Implement a mechanism to send commands to the `tmux` pane (e.g., using `docker exec` to run `tmux send-keys`).
        *   Implement a mechanism to capture and stream output from the `tmux` pane (e.g., using `docker exec` to run `tmux capture-pane` or by attaching to the `tmux` session's output).
        *   Implement the custom `PS1` parsing logic (similar to `bash.py`) to extract command output, exit codes, and current working directory.
        *   Handle interactive input (e.g., `C-c`, `C-d`) by sending appropriate `tmux` control sequences.
    *   [ ] **File System Synchronization:** Ensure the host's working directory is correctly mounted into the sandbox container and accessible by the `tmux` session.

### Phase 3: Advanced Features & Performance Optimization
*   **Objective:** Add advanced features (VSCode integration) and optimize for performance.
*   **Tasks:**
    *   [ ] **VSCode Integration:** Expose the OpenVSCode Server running inside the sandbox container by mapping its port to the host.
    *   [ ] **Environment Parity:** Ensure the Go runtime's sandbox environment closely mirrors the dependencies and configurations (Python, Poetry, Micromamba, Playwright, etc.) defined in OpenHands' `Dockerfile.j2`. This might involve creating a custom Dockerfile for the Go runtime if the OpenHands one is too tightly coupled to Python.
    *   [ ] Conduct performance benchmarks against `All-Hands-AI/OpenHands` to identify bottlenecks in `openhands-runtime-go`.
    *   [ ] Optimize critical paths for performance, focusing on command execution, container startup, and file operations.

### Phase 4: Testing and Validation
*   **Objective:** Ensure functional and API parity, and performance targets are met.
*   **Tasks:**
    *   [ ] Develop comprehensive integration tests to verify API compatibility and functional equivalence with `All-Hands-AI/OpenHands`.
    *   [ ] Develop performance tests to validate optimizations.
    *   [ ] Address any discrepancies found during testing.

## Commit Strategy:
*   Commit often with clear, concise messages.
*   Each commit should represent a logical, atomic change.
*   Push changes frequently to a new branch.

