# Trusted Mode Security Notes

This implementation keeps trusted mode, with an optional fixed-token guard for user-facing Manager access.

- When Manager starts with `WORKER_TOKEN`, GraphQL HTTP, GraphQL-compatible subscriptions, `/terminal/**`, and `/proxy/**` require the same token. Callers can send it as `Authorization: Bearer <token>`, `X-Manager-Token`, or `token` query parameter.
- `GET /auth/status` reports whether token entry is required. `POST /auth/verify` validates the fixed token but does not create a server-side session.
- Browser UI stores the token in `sessionStorage`, so it survives refreshes but is cleared when the tab session ends.
- Worker WebSocket endpoints, including `/worker/ws` and `/worker/frp`, continue to require a `token` query parameter only when Manager is started with `WORKER_TOKEN`.
- If `WORKER_TOKEN` is empty, Manager user-facing endpoints and Worker WebSocket endpoints remain open for compatibility.
- `/proxy/**` forwards HTTP requests to Worker-local or Worker-network `host:port` targets through the FRP tunnel, so trusted callers can reach services visible from Worker machines. Header-based callers can set `worker_host`; browser callers can use `/proxy/web/<worker>/<host>/<port>/**`. When a browser uses `token` query for Manager access, Manager removes that token before forwarding to the Worker target.
- The task Review API is exposed to users through GraphQL but executes Git reads and writes on the assigned Worker through FRP. Trusted callers can read task diffs and request stage, unstage, discard, hunk patch, and restore operations inside the task worktree. Worker validates paths against its `WorkDir`, and discard creates a backup patch, but this is still a write-capable worktree API.
- Role names are reserved as `Admin`, `Developer`, and `Viewer`, but no runtime authorization checks are enforced.
- Deploy Manager, UI, and Workers only on a trusted local network until a later per-user auth module is added.
- Agent runtime environment variables are masked in UI/API responses and sent to Workers only when a task starts.

Future auth can be added at the existing HTTP middleware and GraphQL context boundary without changing domain behavior.
