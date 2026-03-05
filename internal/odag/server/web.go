package server

import (
	"bytes"
	"embed"
	"fmt"
	"hash/fnv"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const DefaultDevWebDir = "internal/odag/server/web"

var (
	//go:embed web/*
	embeddedWebFS   embed.FS
	devReloadScript = []byte(`<script>(function(){let last="";async function tick(){try{const r=await fetch('/__odag_dev_hash',{cache:'no-store'});if(!r.ok){setTimeout(tick,1200);return;}const h=(await r.text()).trim();if(!last){last=h;}else if(h&&h!==last){location.reload();return;}}catch(_){ }setTimeout(tick,1200);}tick();})();</script>`)
)

type webAssets struct {
	embedded fs.FS
	devMode  bool
	devDir   string
}

func newWebAssets(cfg Config) (*webAssets, error) {
	sub, err := fs.Sub(embeddedWebFS, "web")
	if err != nil {
		return nil, fmt.Errorf("embedded web fs: %w", err)
	}

	assets := &webAssets{embedded: sub}
	if !cfg.DevMode {
		return assets, nil
	}

	devDir := strings.TrimSpace(cfg.WebDir)
	if devDir == "" {
		devDir = DefaultDevWebDir
	}
	devDir = filepath.Clean(devDir)
	info, err := os.Stat(devDir)
	if err != nil {
		return nil, fmt.Errorf("web dev dir %q: %w", devDir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("web dev dir %q is not a directory", devDir)
	}

	assets.devMode = true
	assets.devDir = devDir
	return assets, nil
}

func (w *webAssets) serve(wr http.ResponseWriter, r *http.Request) {
	cleanPath := path.Clean("/" + r.URL.Path)
	assetPath := strings.TrimPrefix(cleanPath, "/")
	if assetPath == "" || assetPath == "." {
		assetPath = "index.html"
	}

	data, servedPath, err := w.readRouteAsset(cleanPath, assetPath)
	if err != nil {
		http.Error(wr, "web ui unavailable", http.StatusInternalServerError)
		return
	}

	if w.devMode {
		wr.Header().Set("Cache-Control", "no-store")
	} else if servedPath == "index.html" || servedPath == "trace.html" {
		wr.Header().Set("Cache-Control", "no-cache")
	}
	if contentType := mime.TypeByExtension(filepath.Ext(servedPath)); contentType != "" {
		wr.Header().Set("Content-Type", contentType)
	}
	if w.devMode && isHTMLAsset(servedPath) {
		data = injectDevReload(data)
	}

	wr.WriteHeader(http.StatusOK)
	_, _ = wr.Write(data)
}

func (w *webAssets) readRouteAsset(cleanPath string, assetPath string) ([]byte, string, error) {
	data, err := w.readFile(assetPath)
	if err == nil {
		return data, assetPath, nil
	}

	fallback := "index.html"
	if strings.HasPrefix(cleanPath, "/traces/") {
		fallback = "trace.html"
	}
	data, err = w.readFile(fallback)
	if err != nil {
		return nil, "", err
	}
	return data, fallback, nil
}

func (w *webAssets) readFile(assetPath string) ([]byte, error) {
	if w.devMode {
		fullPath := filepath.Join(w.devDir, filepath.FromSlash(assetPath))
		return os.ReadFile(fullPath)
	}
	return fs.ReadFile(w.embedded, assetPath)
}

func (w *webAssets) devHash() (string, error) {
	if !w.devMode {
		return "", fmt.Errorf("dev mode disabled")
	}

	h := fnv.New64a()
	rows := make([]string, 0, 32)
	walkErr := filepath.WalkDir(w.devDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(d.Name()))
		switch ext {
		case ".html", ".js", ".css":
		default:
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(w.devDir, p)
		if err != nil {
			return err
		}
		rows = append(rows, fmt.Sprintf("%s:%d:%d", filepath.ToSlash(rel), info.ModTime().UnixNano(), info.Size()))
		return nil
	})
	if walkErr != nil {
		return "", walkErr
	}

	sort.Strings(rows)
	for _, row := range rows {
		_, _ = h.Write([]byte(row))
		_, _ = h.Write([]byte{0})
	}
	return strconv.FormatUint(h.Sum64(), 16), nil
}

func isHTMLAsset(assetPath string) bool {
	ext := strings.ToLower(filepath.Ext(assetPath))
	return ext == ".html"
}

func injectDevReload(data []byte) []byte {
	if bytes.Contains(data, []byte("/__odag_dev_hash")) {
		return data
	}
	idx := bytes.LastIndex(data, []byte("</body>"))
	if idx < 0 {
		out := make([]byte, 0, len(data)+len(devReloadScript))
		out = append(out, data...)
		out = append(out, devReloadScript...)
		return out
	}
	out := make([]byte, 0, len(data)+len(devReloadScript))
	out = append(out, data[:idx]...)
	out = append(out, devReloadScript...)
	out = append(out, data[idx:]...)
	return out
}
