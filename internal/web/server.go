// Package web serves JellyTrim's user interface: server-rendered templ pages
// with HTMX for partial updates. Handlers stay thin: they parse the request,
// call a service and render a view.
package web

import (
	"context"
	"embed"
	"io/fs"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"

	"github.com/freakyturtle/jellytrim/internal/store"
	"github.com/freakyturtle/jellytrim/internal/version"
)

//go:embed static
var staticFS embed.FS

// Deps are the services the web layer calls.
type Deps struct {
	Store *store.Store
	Log   *slog.Logger
}

// Server holds the HTTP handlers.
type Server struct {
	Deps
	mux *http.ServeMux
}

// New builds the server and its routes.
func New(d Deps) *Server {
	if d.Log == nil {
		d.Log = slog.Default()
	}
	s := &Server{Deps: d, mux: http.NewServeMux()}
	s.routes()
	return s
}

// Handler returns the full middleware chain.
func (s *Server) Handler() http.Handler {
	// Rejects state-changing requests that a browser sends from another origin.
	cop := http.NewCrossOriginProtection()
	return s.recoverer(s.logRequests(cop.Handler(s.requireSetup(s.mux))))
}

func (s *Server) routes() {
	static, _ := fs.Sub(staticFS, "static")
	s.mux.Handle("GET /static/", http.StripPrefix("/static/", cacheStatic(http.FileServerFS(static))))
	s.mux.HandleFunc("GET /healthz", s.healthz)
	s.mux.HandleFunc("GET /readyz", s.readyz)

	s.mux.HandleFunc("GET /{$}", s.dashboard)
	s.mux.HandleFunc("GET /library", s.library)
	s.mux.HandleFunc("GET /policies", s.policies)
	s.mux.HandleFunc("GET /queue", s.queue)
	s.mux.HandleFunc("GET /history", s.history)
	s.mux.HandleFunc("GET /settings", s.settings)
	s.mux.HandleFunc("GET /setup", s.setup)
	s.mux.HandleFunc("GET /styleguide", s.styleguide)
}

func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok\n"))
}

func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.Store.Ping(ctx); err != nil {
		http.Error(w, "database unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ready\n"))
}

// requireSetup sends every page to the setup wizard until setup is complete.
func (s *Server) requireSetup(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if p == "/healthz" || p == "/readyz" || strings.HasPrefix(p, "/static/") || strings.HasPrefix(p, "/setup") || p == "/styleguide" {
			next.ServeHTTP(w, r)
			return
		}
		st, err := s.Store.Settings(r.Context())
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		if !st.SetupComplete {
			redirect(w, r, "/setup")
			return
		}
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		if r.URL.Path == "/healthz" || strings.HasPrefix(r.URL.Path, "/static/") {
			return
		}
		// Only the path is logged: query strings could carry user input.
		s.Log.Debug("http: request", "method", r.Method, "path", r.URL.Path, "status", rec.status, "duration", time.Since(start))
	})
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				s.Log.Error("http: panic", "path", r.URL.Path, "panic", v, "stack", string(debug.Stack()))
				http.Error(w, "Internal error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func cacheStatic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Asset URLs carry ?v=<version>, so a long cache is safe.
		if r.URL.Query().Get("v") != "" {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		next.ServeHTTP(w, r)
	})
}

// render writes a templ component as a full page or HTMX fragment.
func (s *Server) render(w http.ResponseWriter, r *http.Request, c templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := c.Render(r.Context(), w); err != nil {
		s.Log.Error("http: render", "path", r.URL.Path, "err", err)
	}
}

func (s *Server) serverError(w http.ResponseWriter, r *http.Request, err error) {
	s.Log.Error("http: handler", "path", r.URL.Path, "err", err)
	http.Error(w, "Something went wrong. The details are in JellyTrim's log.", http.StatusInternalServerError)
}

// redirect works for both normal requests and HTMX requests.
func redirect(w http.ResponseWriter, r *http.Request, to string) {
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", to)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// startedAt busts the static cache on every restart of a development build.
var startedAt = strconv.FormatInt(time.Now().Unix(), 36)

// assetVersion is appended to static URLs so browsers refetch after upgrades.
func assetVersion() string {
	if version.Version == "dev" {
		return "dev-" + startedAt
	}
	return version.Version
}
