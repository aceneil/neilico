package dashboard

import (
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

type Options struct {
	Dir string
	SPA bool
}

type Handler struct {
	dir string
	spa bool
}

func New(options Options) http.Handler {
	return &Handler{dir: filepath.Clean(options.Dir), spa: options.SPA}
}

// Guard rejects encoded or literal parent traversal before net/http's ServeMux
// can normalize it into a redirect.
func Guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !SafePath(r.URL.Path) {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func SafePath(requestPath string) bool {
	if strings.ContainsRune(requestPath, 0) {
		return false
	}
	for _, segment := range strings.Split(strings.ReplaceAll(requestPath, "\\", "/"), "/") {
		if segment == ".." {
			return false
		}
	}
	return true
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.NotFound(w, r)
		return
	}
	if !SafePath(r.URL.Path) {
		http.NotFound(w, r)
		return
	}

	relative := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if relative == "" || relative == "." {
		relative = "index.html"
	}
	file, info, err := h.open(relative)
	if err == nil {
		defer file.Close()
		h.serveFile(w, r, file, info, relative, false)
		return
	}
	if err != nil {
		if !os.IsNotExist(err) {
			http.NotFound(w, r)
			return
		}
	}
	if h.spa && !looksLikeStaticResource(r.URL.Path) {
		index, indexInfo, indexErr := h.open("index.html")
		if indexErr != nil {
			http.NotFound(w, r)
			return
		}
		defer index.Close()
		h.serveFile(w, r, index, indexInfo, "index.html", true)
		return
	}
	http.NotFound(w, r)
}

func (h *Handler) open(relative string) (*os.File, os.FileInfo, error) {
	clean := filepath.Clean(filepath.FromSlash(relative))
	if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return nil, nil, os.ErrNotExist
	}
	fullPath := filepath.Join(h.dir, clean)
	relativeToRoot, err := filepath.Rel(h.dir, fullPath)
	if err != nil || relativeToRoot == ".." || strings.HasPrefix(relativeToRoot, ".."+string(filepath.Separator)) {
		return nil, nil, os.ErrNotExist
	}
	resolvedRoot, err := filepath.EvalSymlinks(h.dir)
	if err != nil {
		return nil, nil, err
	}
	resolvedPath, err := filepath.EvalSymlinks(fullPath)
	if err != nil {
		return nil, nil, err
	}
	relativeResolved, err := filepath.Rel(resolvedRoot, resolvedPath)
	if err != nil || relativeResolved == ".." || strings.HasPrefix(relativeResolved, ".."+string(filepath.Separator)) {
		return nil, nil, os.ErrNotExist
	}
	file, err := os.Open(fullPath)
	if err != nil {
		return nil, nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		file.Close()
		return nil, nil, os.ErrNotExist
	}
	return file, info, nil
}

func (h *Handler) serveFile(w http.ResponseWriter, r *http.Request, file *os.File, info os.FileInfo, relative string, spaFallback bool) {
	extension := strings.ToLower(filepath.Ext(relative))
	contentType := contentTypes[extension]
	if contentType == "" {
		contentType = mime.TypeByExtension(extension)
	}
	if contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	switch {
	case relative == "index.html" || spaFallback:
		w.Header().Set("Cache-Control", "no-cache")
	case strings.HasPrefix(strings.TrimPrefix(filepath.ToSlash(relative), "/"), "assets/"):
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	http.ServeContent(w, r, info.Name(), info.ModTime(), file)
}

func looksLikeStaticResource(requestPath string) bool {
	extension := strings.ToLower(path.Ext(requestPath))
	return extension != "" && extension != ".html"
}

var contentTypes = map[string]string{
	".css":   "text/css; charset=utf-8",
	".gif":   "image/gif",
	".html":  "text/html; charset=utf-8",
	".ico":   "image/x-icon",
	".jpeg":  "image/jpeg",
	".jpg":   "image/jpeg",
	".js":    "text/javascript; charset=utf-8",
	".json":  "application/json",
	".map":   "application/json",
	".png":   "image/png",
	".svg":   "image/svg+xml",
	".txt":   "text/plain; charset=utf-8",
	".wasm":  "application/wasm",
	".webp":  "image/webp",
	".woff":  "font/woff",
	".woff2": "font/woff2",
}
