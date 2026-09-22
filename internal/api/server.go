package api

import (
	"context"
	"io"
	"net/http"
	"time"
)

type Config struct {
	Addr         string
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	IdleTimeout  time.Duration
}

type Server struct {
	httpServer *http.Server
	mux        *http.ServeMux
}

// statusResponseWriter captures HTTP status code for metrics recording
// while preserving optional standard ResponseWriter interfaces.
type statusResponseWriter struct {
	http.ResponseWriter
	statusCode int
	written    bool
}

func (w *statusResponseWriter) WriteHeader(code int) {
	if !w.written {
		w.statusCode = code
		w.written = true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusResponseWriter) Write(b []byte) (int, error) {
	if !w.written {
		w.statusCode = http.StatusOK
		w.written = true
	}
	return w.ResponseWriter.Write(b)
}

func (w *statusResponseWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *statusResponseWriter) ReadFrom(r io.Reader) (int64, error) {
	if rf, ok := w.ResponseWriter.(io.ReaderFrom); ok {
		if !w.written {
			w.statusCode = http.StatusOK
			w.written = true
		}
		return rf.ReadFrom(r)
	}
	return io.Copy(w.ResponseWriter, r)
}

func NewServer(cfg Config, handlers *Handlers) *Server {
	mux := http.NewServeMux()

	wrap := func(pattern string, fn http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			srw := &statusResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}
			fn(srw, r)
			handlers.recorder.RecordHTTPRequest(r.Method, pattern, srw.statusCode, time.Since(start))
		}
	}

	mux.HandleFunc("POST /monitors", wrap("/monitors", handlers.CreateMonitor))
	mux.HandleFunc("GET /monitors", wrap("/monitors", handlers.ListMonitors))
	mux.HandleFunc("GET /monitors/{id}", wrap("/monitors/{id}", handlers.GetMonitor))
	mux.HandleFunc("PATCH /monitors/{id}", wrap("/monitors/{id}", handlers.PatchMonitor))
	mux.HandleFunc("DELETE /monitors/{id}", wrap("/monitors/{id}", handlers.DeleteMonitor))
	mux.HandleFunc("GET /monitors/{id}/status", wrap("/monitors/{id}/status", handlers.GetMonitorStatus))
	mux.HandleFunc("GET /monitors/{id}/checks", wrap("/monitors/{id}/checks", handlers.GetMonitorChecks))
	mux.HandleFunc("GET /livez", wrap("/livez", handlers.Livez))
	mux.HandleFunc("GET /readyz", wrap("/readyz", handlers.Readyz))
	mux.HandleFunc("GET /metrics", handlers.Metrics)

	readTimeout := cfg.ReadTimeout
	if readTimeout <= 0 {
		readTimeout = 5 * time.Second
	}
	writeTimeout := cfg.WriteTimeout
	if writeTimeout <= 0 {
		writeTimeout = 10 * time.Second
	}
	idleTimeout := cfg.IdleTimeout
	if idleTimeout <= 0 {
		idleTimeout = 120 * time.Second
	}

	srv := &http.Server{
		Addr:         cfg.Addr,
		Handler:      mux,
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
		IdleTimeout:  idleTimeout,
	}

	return &Server{
		httpServer: srv,
		mux:        mux,
	}
}

func (s *Server) Start() error {
	return s.httpServer.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

func (s *Server) Handler() http.Handler {
	return s.mux
}
