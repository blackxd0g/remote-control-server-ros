package webui

import (
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
)

//go:embed dist
var embedded embed.FS

func Handler() http.Handler {
	assets, err := fs.Sub(embedded, "dist")
	if err != nil {
		panic(err)
	}
	files := http.FileServer(http.FS(assets))
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/api" || strings.HasPrefix(request.URL.Path, "/api/") ||
			request.URL.Path == "/internal" || strings.HasPrefix(request.URL.Path, "/internal/") ||
			request.URL.Path == "/healthz" {
			http.NotFound(response, request)
			return
		}
		clean := strings.TrimPrefix(path.Clean(request.URL.Path), "/")
		if clean != "." && clean != "" {
			if info, statErr := fs.Stat(assets, clean); statErr == nil && !info.IsDir() {
				if strings.HasPrefix(clean, "assets/") {
					response.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				if contentType := mime.TypeByExtension(path.Ext(clean)); contentType != "" {
					response.Header().Set("Content-Type", contentType)
				}
				files.ServeHTTP(response, request)
				return
			}
		}
		response.Header().Set("Cache-Control", "no-store")
		request.URL.Path = "/"
		files.ServeHTTP(response, request)
	})
}
