// Package webui embeds the built admin UI into the binary so that jarvis.exe
// ships as a single file.
//
// dist/ holds the Vite build output. A placeholder page is committed so the
// tree always compiles before the frontend exists; the real build overwrites
// it (see web/README.md).
package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var distFS embed.FS

// FS returns the embedded assets rooted at dist/.
func FS() fs.FS {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		// Only reachable if the embed directive and this path disagree, which
		// is a build-time mistake rather than a runtime condition.
		panic(err)
	}
	return sub
}

// Handler serves the UI, falling back to index.html for unknown paths so that
// client-side routing works on a hard refresh.
func Handler() http.Handler {
	assets := FS()
	files := http.FileServer(http.FS(assets))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if p == "" {
			p = "index.html"
		}
		if _, err := fs.Stat(assets, p); err != nil {
			serveIndex(w, r, assets)
			return
		}
		files.ServeHTTP(w, r)
	})
}

func serveIndex(w http.ResponseWriter, r *http.Request, assets fs.FS) {
	data, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		http.Error(w, "admin UI is not bundled in this build", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}
