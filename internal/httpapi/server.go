package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"mayank2/internal/events"
	"mayank2/internal/queue"
)

// Pauser is the pause/resume surface used by POST /api/pause and /api/resume.
// *queue.Queue satisfies it.
type Pauser interface {
	PauseAll(ctx context.Context) error
	ResumeAll(ctx context.Context) error
	PauseResource(ctx context.Context, res queue.Resource) error
	ResumeResource(ctx context.Context, res queue.Resource) error
}

// Options configures a Server.
type Options struct {
	// DashboardToken is the plaintext DASHBOARD_TOKEN from the environment.
	// Only its SHA-256 is retained; the plaintext is not stored on Server.
	DashboardToken string
	// DB is the migrated SQLite handle. Required for approvals, jobs, pause flags.
	DB *sql.DB
	// Events fans out live events to GET /api/events/stream. Optional: stream
	// still works but emits nothing until a Bus is wired.
	Events *events.Bus
	// Queue backs pause/resume for resource-class scopes. Optional.
	Queue Pauser
	// Version reported by GET /api/health.
	Version string
	// Logger defaults to slog.Default().
	Logger *slog.Logger
	// WebFS overrides the embedded dashboard filesystem (tests).
	WebFS fs.FS
}

// Server is the dashboard HTTP API (SPEC.md §4, ARCHITECTURE.md §7).
type Server struct {
	db        *sql.DB
	events    *events.Bus
	queue     Pauser
	version   string
	log       *slog.Logger
	tokenHash [32]byte
	sessions  *sessionStore
	logins    *loginLimiter
	web       fs.FS
	handler   http.Handler

	mu       sync.Mutex
	servers  []*http.Server
	listener []net.Listener
}

// New builds a Server. DashboardToken must be non-empty.
func New(opts Options) (*Server, error) {
	if strings.TrimSpace(opts.DashboardToken) == "" {
		return nil, fmt.Errorf("httpapi: DashboardToken is required")
	}
	if opts.DB == nil {
		return nil, fmt.Errorf("httpapi: DB is required")
	}
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	version := opts.Version
	if version == "" {
		version = "dev"
	}
	web := opts.WebFS
	if web == nil {
		var err error
		web, err = distSubFS()
		if err != nil {
			return nil, err
		}
	}
	s := &Server{
		db:        opts.DB,
		events:    opts.Events,
		queue:     opts.Queue,
		version:   version,
		log:       log,
		tokenHash: hashToken(opts.DashboardToken),
		sessions:  newSessionStore(),
		logins:    newLoginLimiter(),
		web:       web,
	}
	s.handler = s.routes()
	return s, nil
}

// Handler returns the root HTTP handler (API + SPA). Useful for httptest.
func (s *Server) Handler() http.Handler { return s.handler }

// ListenAndServe resolves listen addresses and serves on each until ctx is
// cancelled. It binds only to addresses ResolveListen accepts.
func (s *Server) ListenAndServe(ctx context.Context, listen []string) error {
	addrs, err := ResolveListen(listen)
	if err != nil {
		return err
	}
	errCh := make(chan error, len(addrs))
	for _, addr := range addrs {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			s.shutdown()
			return fmt.Errorf("httpapi: listen %s: %w", addr, err)
		}
		srv := &http.Server{
			Handler:           s.handler,
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       30 * time.Second,
			// WriteTimeout left unset so SSE streams can stay open.
			BaseContext: func(net.Listener) context.Context { return ctx },
		}
		s.mu.Lock()
		s.servers = append(s.servers, srv)
		s.listener = append(s.listener, ln)
		s.mu.Unlock()

		s.log.Info("httpapi listening", "addr", ln.Addr().String())
		go func(srv *http.Server, ln net.Listener) {
			err := srv.Serve(ln)
			if err != nil && err != http.ErrServerClosed {
				errCh <- err
				return
			}
			errCh <- nil
		}(srv, ln)
	}

	select {
	case <-ctx.Done():
		s.shutdown()
		for range addrs {
			<-errCh
		}
		return ctx.Err()
	case err := <-errCh:
		s.shutdown()
		for i := 1; i < len(addrs); i++ {
			<-errCh
		}
		return err
	}
}

func (s *Server) shutdown() {
	s.mu.Lock()
	servers := append([]*http.Server(nil), s.servers...)
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, srv := range servers {
		_ = srv.Shutdown(ctx)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(true)
	_ = enc.Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
