# Trusted Mode Security Notes

This implementation keeps trusted mode, with an optional fixed-token guard for user-facing Manager access.

- When Manager starts with `WORKER_TOKEN`, GraphQL HTTP, GraphQL-compatible subscriptions, `/terminal/**`, and `/proxy/**` require the same token. Callers can send it as `Authorization: Bearer <token>`, `X-Manager-Token`, or `token` query parameter.
- `GET /auth/status` reports whether token entry is required. `POST /auth/verify` validates the fixed token but does not create a server-side session.
- Browser UI stores the token in `sessionStorage`, so it survives refreshes but is cleared when the tab session ends.
- Worker registration/heartbeat at `/worker/ws` and the FRP tunnel at `/worker/frp` require a `token` query parameter when Manager is started with `WORKER_TOKEN`. The WebSocket does not carry task commands or execution events.
- Worker exposes its A2A HTTP server only on `127.0.0.1` or `::1`. Manager can reach it only through the authenticated Worker FRP tunnel, validates the advertised Agent Card endpoint against registered capabilities, and sends `Authorization: Bearer <WORKER_TOKEN>` to `/a2a` when a token is configured. `WORKER_TOKEN` must have the same value in both Manager and Worker processes.
- If `WORKER_TOKEN` is empty on both Manager and Worker, Manager user-facing endpoints, Worker registration/FRP, and the loopback A2A endpoint remain open for local compatibility. A token configured on only one side is invalid and prevents registration or A2A authentication. Network isolation remains mandatory because tokenless mode has no caller identity.
- `/proxy/**` forwards HTTP requests to Worker-local or Worker-network `host:port` targets through the FRP tunnel, so trusted callers can reach services visible from Worker machines. Header-based callers can set `worker_host`; browser callers can use `/proxy/web/<worker>/<host>/<port>/**`. When a browser uses `token` query for Manager access, Manager removes that token before forwarding to the Worker target.
- The task Review API is exposed to users through GraphQL but executes Git reads and writes on the assigned Worker through FRP. Trusted callers can read task diffs and request stage, unstage, discard, hunk patch, and restore operations inside the task worktree. Worker validates paths against its `WorkDir`, and discard creates a backup patch, but this is still a write-capable worktree API.
- Role names are reserved as `Admin`, `Developer`, and `Viewer`, but no runtime authorization checks are enforced.
- Deploy Manager, UI, and Workers only on a trusted local network until a later per-user auth module is added.
- Agent runtime environment variables are masked in UI/API responses. Manager injects plaintext values only into the transient A2A execution request; dispatch intents, A2A Tasks, Artifacts, event journals, diagnostics, logs, and errors persist placeholders rather than secret values.
- Adapter authentication detection keeps only a fixed 64 KiB rolling output window plus a matched flag, so long-running Agent output is not buffered without bound. Approval interactions require an explicit kind-compatible decision at Manager and are rejected again by the Codex Adapter if the decision is empty or unknown.

Future auth can be added at the existing HTTP middleware and GraphQL context boundary without changing domain behavior.
