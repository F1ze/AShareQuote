// Package httpapi exposes the local REST/SSE API and embedded control panel.
package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"time"

	"quotes/internal/dialog"
	"quotes/internal/download"
	"quotes/internal/lines"
)

type Manager = download.Manager
type DownloadOptions = download.DownloadOptions

type Server struct {
	manager  *download.Manager
	provider lines.Provider
	assets   fs.FS
}

func NewServer(manager *download.Manager, provider lines.Provider, assets fs.FS) *Server {
	return &Server{manager: manager, provider: provider, assets: assets}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/defaults", s.handleDefaults)
	mux.HandleFunc("GET /api/task", s.handleTask)
	mux.HandleFunc("POST /api/task", s.handleStart)
	mux.HandleFunc("DELETE /api/task", s.handleCancel)
	mux.HandleFunc("POST /api/hosts/test", s.handleHostTest)
	mux.HandleFunc("POST /api/dialog/folder", s.handleDialog("folder"))
	mux.HandleFunc("POST /api/dialog/gbbq", s.handleDialog("gbbq"))
	mux.HandleFunc("GET /api/preview", s.handlePreview)
	mux.HandleFunc("GET /api/events", s.handleEvents)
	mux.Handle("GET /", http.FileServer(http.FS(s.assets)))
	return securityHeaders(mux)
}

func (s *Server) handleDialog(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
		defer cancel()
		path, err := dialog.PickPath(ctx, kind)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"path": path})
	}
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; font-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleDefaults(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"lineSource": "builtin",
		"gbbqPath":   "",
		"outputDir":  s.manager.DefaultOutput(),
		"category":   9,
		"count":      800,
		"workers":    12,
		"timeout":    8,
		"markets":    []uint8{0, 1},
		"task":       s.manager.Snapshot(),
	})
}

func (s *Server) handleTask(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.manager.Snapshot())
}

func (s *Server) handleStart(w http.ResponseWriter, r *http.Request) {
	var options DownloadOptions
	if err := decodeJSON(w, r, &options); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	snapshot, err := s.manager.Start(options)
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusAccepted, snapshot)
}

func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
	if err := s.manager.Cancel(); err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusOK, s.manager.Snapshot())
}

type hostTestRequest struct {
	Timeout int `json:"timeout"`
}

func (s *Server) handleHostTest(w http.ResponseWriter, r *http.Request) {
	var request hostTestRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if request.Timeout == 0 {
		request.Timeout = 8
	}
	if request.Timeout < 2 || request.Timeout > 60 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("超时须为 2–60 秒"))
		return
	}
	_, report, err := s.provider.Available(r.Context(), time.Duration(request.Timeout)*time.Second)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("当前服务不支持事件流"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	ticker := time.NewTicker(400 * time.Millisecond)
	defer ticker.Stop()
	send := func() error {
		payload, err := json.Marshal(s.manager.Snapshot())
		if err != nil {
			return err
		}
		if _, err = fmt.Fprintf(w, "event: task\ndata: %s\n\n", payload); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}
	if err := send(); err != nil {
		return
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			if err := send(); err != nil {
				return
			}
		}
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("请求内容无效：%w", err)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
