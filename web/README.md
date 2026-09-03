# Admin UI

The React admin UI lands here in phase P2. Until then a static placeholder page
is served so that `jarvis run` has something to show and the embed directive
always has a target.

## How the UI gets into the binary

`internal/webui` embeds `internal/webui/dist` with `//go:embed all:dist`, so the
production build must write Vite's output to that directory rather than the
conventional `web/dist`:

```jsonc
// vite.config.ts (P2)
build: {
  outDir: "../internal/webui/dist",
  emptyOutDir: true,
}
```

`internal/webui/dist/index.html` is committed on purpose. Go refuses to compile
a package whose `go:embed` pattern matches nothing, so removing it would break
`go build` for anyone who has not run the frontend build yet.

## Development loop

Run the Go core and the Vite dev server side by side and proxy API calls:

```jsonc
// vite.config.ts (P2)
server: {
  proxy: {
    "/api": "http://127.0.0.1:9527",
  },
}
```
