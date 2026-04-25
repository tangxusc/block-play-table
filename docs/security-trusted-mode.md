# Trusted Mode Security Notes

This implementation intentionally runs in trusted mode.

- GraphQL HTTP, GraphQL-compatible subscriptions, and Worker WebSocket endpoints do not require `Authorization`, `X-API-Key`, `X-User-Role`, or Worker token headers.
- Role names are reserved as `Admin`, `Developer`, and `Viewer`, but no runtime authorization checks are enforced.
- Deploy Manager, UI, and Workers only on a trusted local network until a later auth module is added.
- Agent runtime environment variables are masked in UI/API responses and sent to Workers only when a task starts.

Future auth can be added at the existing HTTP middleware and GraphQL context boundary without changing domain behavior.
