package api

import (
	"context"
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

func NewServer(cfg Config, handlers *Handlers) *Server {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /monitors", handlers.CreateMonitor)
	mux.HandleFunc("GET /monitors", handlers.ListMonitors)
	mux.HandleFunc("GET /monitors/{id}", handlers.GetMonitor)
	mux.HandleFunc("PATCH /monitors/{id}", handlers.PatchMonitor)
	mux.HandleFunc("DELETE /monitors/{id}", handlers.DeleteMonitor)
	mux.HandleFunc("GET /monitors/{id}/status", handlers.GetMonitorStatus)
	mux.HandleFunc("GET /monitors/{id}/checks", handlers.GetMonitorChecks)

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
