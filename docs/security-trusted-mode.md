# Trusted Mode Security Notes

This implementation intentionally keeps the user-facing API in trusted mode.

- GraphQL HTTP and GraphQL-compatible subscriptions do not require `Authorization`, `X-API-Key`, or `X-User-Role` headers.
- Worker WebSocket endpoints, including `/worker/ws` and `/worker/frp`, require a `token` query parameter only when Manager is started with `WORKER_TOKEN`.
- `/proxy/**` forwards HTTP requests to Worker-local `127.0.0.1:<worker_port>` through the FRP tunnel, so trusted callers can reach services listening on Worker machines.
- Role names are reserved as `Admin`, `Developer`, and `Viewer`, but no runtime authorization checks are enforced.
- Deploy Manager, UI, and Workers only on a trusted local network until a later auth module is added.
- Agent runtime environment variables are masked in UI/API responses and sent to Workers only when a task starts.

Future auth can be added at the existing HTTP middleware and GraphQL context boundary without changing domain behavior.
