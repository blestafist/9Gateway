package httpserver

import (
	"errors"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/pestit/9gateway/web"
)

// uiHandler serves embedded static UI assets under /ui/.
type uiHandler struct {
	fs fs.FS
}

func newUIHandler(staticFS fs.FS) http.Handler {
	if staticFS == nil {
		staticFS = web.Dist()
	}
	return &uiHandler{fs: staticFS}
}

func (h *uiHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	setSecurityHeaders(w)

	// Only GET and HEAD methods are supported for UI serving.
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	// Exact /ui must redirect to /ui/
	if r.URL.Path == "/ui" {
		target := "/ui/"
		if q := r.URL.RawQuery; q != "" {
			target += "?" + q
		}
		http.Redirect(w, r, target, http.StatusMovedPermanently)
		return
	}

	// UI assets and SPA fallback only exist below /ui/
	if !strings.HasPrefix(r.URL.Path, "/ui/") {
		http.NotFound(w, r)
		return
	}

	relPath := strings.TrimPrefix(r.URL.Path, "/ui/")
	relPath = path.Clean("/" + relPath)
	relPath = strings.TrimPrefix(relPath, "/")

	if relPath == "" || relPath == "." {
		h.serveSPAIndex(w, r)
		return
	}

	isHashedAsset := strings.HasPrefix(relPath, "assets/")

	file, err := h.fs.Open(relPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			if isHashedAsset {
				// Missing hashed assets must 404 rather than falling back to index.html
				http.NotFound(w, r)
				return
			}
			// SPA fallback: client-side routes such as /ui/example serve index.html
			h.serveSPAIndex(w, r)
			return
		}
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	if stat.IsDir() {
		h.serveSPAIndex(w, r)
		return
	}

	if isHashedAsset {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else if relPath == "index.html" {
		w.Header().Set("Cache-Control", "no-cache")
	}

	contentType := mimeTypeForPath(relPath)
	if contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}

	seeker, ok := file.(io.ReadSeeker)
	if !ok {
		data, readErr := io.ReadAll(file)
		if readErr != nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
		return
	}

	http.ServeContent(w, r, stat.Name(), stat.ModTime(), seeker)
}

func (h *uiHandler) serveSPAIndex(w http.ResponseWriter, r *http.Request) {
	file, err := h.fs.Open("index.html")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	seeker, ok := file.(io.ReadSeeker)
	if !ok {
		data, readErr := io.ReadAll(file)
		if readErr != nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
		return
	}

	http.ServeContent(w, r, "index.html", stat.ModTime(), seeker)
}

func mimeTypeForPath(filePath string) string {
	ext := strings.ToLower(path.Ext(filePath))
	switch ext {
	case ".html", ".htm":
		return "text/html; charset=utf-8"
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".json", ".map":
		return "application/json"
	case ".svg":
		return "image/svg+xml"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".ico":
		return "image/x-icon"
	case ".woff":
		return "font/woff"
	case ".woff2":
		return "font/woff2"
	case ".ttf":
		return "font/ttf"
	default:
		if detected := mime.TypeByExtension(ext); detected != "" {
			return detected
		}
		return "application/octet-stream"
	}
}
