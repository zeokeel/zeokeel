package gateway

import (
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/zeokeel/zeokeel/internal/gateway/webclient"
)

// Serving the browser client (docs/TRANSPORT.md §3.2, §4.6).
//
// The client is same-origin with the state plane, which is why there is no CORS
// header anywhere in this package. Static assets do not require the token (the
// first paint cannot carry one); Host is still checked, which is what closes
// DNS rebinding.
const (
	// bundleMissingTitle/Page is the readable answer when the binary carries no
	// built client yet (the web track builds web/dist separately).
	bundleMissingTitle = "ZeoKeel gateway: browser client not built"
	bundleMissingBody  = "The browser client bundle is not embedded in this binary.\n\n" +
		"Build it and rebuild the gateway:\n\n" +
		"    scripts/build-web-client.sh      # npm run build in web/ + copy into internal/gateway/webclient/dist\n" +
		"    go build ./cmd/zeokeel\n\n" +
		"The WebSocket control plane is available at /ws in the meantime.\n"
)

// handleStatic serves the embedded client with SPA fallback.
func (g *Gateway) handleStatic(w http.ResponseWriter, r *http.Request) {
	if err := g.auth.checkHost(r); err != nil {
		writeProtocolError(w, http.StatusForbidden, err)
		return
	}
	if !g.clientAvailable() {
		// A readable page beats a 404 (or a panic) while the client track is
		// still building; the tray shell's "open interface" shows it verbatim.
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(bundleMissingTitle + "\n\n" + bundleMissingBody))
		return
	}
	filePath := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if filePath == "" || filePath == "." {
		filePath = "index.html"
	}
	if filePath == "BUNDLE_INFO" || filePath == ".keep" {
		// Build bookkeeping inside the embed tree, not part of the client.
		http.NotFound(w, r)
		return
	}
	if g.serveClientFile(w, r, filePath) {
		return
	}
	// SPA fallback: a client route (/c/:id, /inbox, …) is not a file, so the
	// client entry point answers it and the router takes over. A missing asset
	// (has an extension) is a real 404.
	if looksLikeNavigation(r) {
		if g.serveClientFile(w, r, "index.html") {
			return
		}
	}
	http.NotFound(w, r)
}

// clientFS is the file tree the static handler serves: the embedded client
// bundle, or whatever a test substituted.
func (g *Gateway) clientFS() fs.FS {
	if g.webFS != nil {
		return g.webFS
	}
	return webclient.FS()
}

// clientAvailable reports whether the served tree has a client entry point.
func (g *Gateway) clientAvailable() bool {
	tree := g.clientFS()
	if tree == nil {
		return false
	}
	info, err := fs.Stat(tree, "index.html")
	return err == nil && !info.IsDir()
}

// serveClientFile writes one file from the client tree, reporting whether it
// existed.
func (g *Gateway) serveClientFile(w http.ResponseWriter, r *http.Request, name string) bool {
	content, err := fs.ReadFile(g.clientFS(), name)
	if err != nil {
		return false
	}
	contentType := mime.TypeByExtension(path.Ext(name))
	if contentType == "" {
		contentType = http.DetectContentType(content)
	}
	w.Header().Set("Content-Type", contentType)
	// Vite emits content-hashed asset names, so they are immutable; the entry
	// point must never be cached or a rebuilt client would not be picked up.
	if name == "index.html" {
		w.Header().Set("Cache-Control", "no-cache")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	http.ServeContent(w, r, name, embeddedModTime(), strings.NewReader(string(content)))
	return true
}

// embeddedModTime is the Last-Modified stamp for embedded files. Build output has
// no meaningful mtime, and content hashing already drives caching; the process
// start is the honest floor.
var processStart = time.Now()

// embeddedModTime returns the process start, which is when this binary's
// embedded bundle became immutable.
func embeddedModTime() time.Time { return processStart }

// looksLikeNavigation reports an HTML navigation (SPA fallback) rather than an
// asset request.
func looksLikeNavigation(r *http.Request) bool {
	if strings.Contains(r.Header.Get("Accept"), "text/html") {
		return true
	}
	return !strings.Contains(path.Base(r.URL.Path), ".")
}
