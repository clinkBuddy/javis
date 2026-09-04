# Admin UI

React + TypeScript, built with Vite and embedded into `jarvis.exe`.

## How the UI gets into the binary

`internal/webui` embeds `internal/webui/dist` with `//go:embed all:dist`, so the
production build writes Vite's output there rather than to the conventional
`web/dist`:

```ts
// vite.config.ts
build: {
  outDir: "../internal/webui/dist",
  emptyOutDir: true,
}
```

**The build output is committed.** Go refuses to compile a package whose
`go:embed` pattern matches nothing, so without it `go build ./...` would fail on
any machine that has not run the frontend build. Committing it means the Go
toolchain alone is enough to produce a working `jarvis.exe`; Node is only needed
to change the UI.

After changing anything under `web/src`, run the frontend build and commit the
regenerated `internal/webui/dist` alongside the source change:

```powershell
.\build\build.ps1 -Web
```

## Development loop

Run the Go core and the Vite dev server side by side. `vite.config.ts` proxies
`/api` to `127.0.0.1:9527`, so the UI hot-reloads against the real API:

```powershell
# terminal 1
go run .\cmd\jarvis run

# terminal 2
cd web; npm run dev     # http://127.0.0.1:5173
```

## Structure

| Path                          | Purpose                                          |
| ----------------------------- | ------------------------------------------------ |
| `src/api.ts`                  | Typed REST client and the upload-with-progress   |
| `src/hooks.ts`                | `usePolled` for live state, `useAction` for CRUD |
| `src/Shell.tsx`               | Header, navigation and the liveness indicator    |
| `src/pages/Dashboard.tsx`     | App list, state badges, start/stop/restart       |
| `src/pages/AppDetail.tsx`     | One app: status, artifacts, JVM options          |
| `src/pages/Jdks.tsx`          | JDK registry and host scan                       |
| `src/components/`             | State badge, artifact panel, JVM profile form    |

## Notes

Routing is hash-based (`#/apps/order-api`). The Go handler does fall back to
`index.html`, but hash routes keep the UI working unchanged if it is ever
mounted under a subpath or opened straight from disk.

Views poll rather than use websockets. Managed processes change state outside
the UI — a crash, or a stop issued from another browser — and the payloads are
small enough that polling costs less than the reconnect handling a socket needs.

If `npm install` fails with `ERR_INVALID_ARG_TYPE: The "file" argument must be
of type string`, the shell is missing `ComSpec`. npm needs it to run package
install scripts on Windows:

```powershell
$env:ComSpec = "C:\Windows\System32\cmd.exe"
```
