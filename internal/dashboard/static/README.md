# Dashboard static assets

These files are vendored to keep the build pipeline npm-free.

| File | Upstream | Version | Why vendored |
|---|---|---|---|
| `htmx.min.js` | https://unpkg.com/htmx.org | 2.0.4 | Core dashboard interactivity |
| `htmx-ext-sse.js` | https://unpkg.com/htmx-ext-sse | 2.2.2 | SSE swap for live tail |

## Upgrade procedure

1. Download new version from unpkg.
2. Update the version in this README and in the audit comment at the top of the file.
3. Run `go test ./...` and `go test -tags=integration ./test/integration/...`.
4. Eyeball the dashboard against a running daemon.

No automated upgrades; htmx is reviewed manually.
