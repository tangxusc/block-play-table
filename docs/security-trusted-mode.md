# Trusted Mode Security Notes

This implementation intentionally keeps the user-facing API in trusted mode.

- GraphQL HTTP and GraphQL-compatible subscriptions do not require `Authorization`, `X-API-Key`, or `X-User-Role` headers.
- Worker WebSocket endpoints, including `/worker/ws` and `/worker/frp`, require a `token` query parameter only when Manager is started with `WORKER_TOKEN`.
- `/proxy/**` forwards HTTP requests to Worker-local or Worker-network `host:port` targets through the FRP tunnel, so trusted callers can reach services visible from Worker machines. Header-based callers can set `worker_host`; browser callers can use `/proxy/web/<worker>/<host>/<port>/**`.
- The task Review API is exposed to users through GraphQL but executes Git reads and writes on the assigned Worker through FRP. Trusted callers can read task diffs and request stage, unstage, discard, hunk patch, and restore operations inside the task worktree. Worker validates paths against its `WorkDir`, and discard creates a backup patch, but this is still a write-capable worktree API.
- Role names are reserved as `Admin`, `Developer`, and `Viewer`, but no runtime authorization checks are enforced.
- Deploy Manager, UI, and Workers only on a trusted local network until a later auth module is added.
- Agent runtime environment variables are masked in UI/API responses and sent to Workers only when a task starts.

Future auth can be added at the existing HTTP middleware and GraphQL context boundary without changing domain behavior.
