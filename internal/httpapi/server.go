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

	"mayank2/internal/content"
	"mayank2/internal/events"
	"mayank2/internal/queue"
)

// ApprovalDecider applies human approve/reject/redo (content.ApprovalService).
type ApprovalDecider interface {
	Decide(ctx context.Context, req content.DecideRequest) error
}

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
	// Approvals applies decisions (schedules publications on approve).
	// Required for POST .../decision; nil fails closed.
	Approvals ApprovalDecider
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
	approvals ApprovalDecider
	version   string
	log       *slog.Logger
	tokenHash [32]byte
	sessions  *sessionStore
	logins    *loginLimiter
	web       fs.FS
	handler   http.Handler

	mu         sync.Mutex
	servers    []*http.Server
	listener   []net.Listener
	boundAddrs map[string]bool
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
		var (
			source string
			err    error
		)
		web, source, err = distSubFS()
		if err != nil {
			return nil, err
		}
		log.Info("httpapi: serving dashboard", "source", source)
	}
	s := &Server{
		db:        opts.DB,
		events:    opts.Events,
		queue:     opts.Queue,
		approvals: opts.Approvals,
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
// cancelled. It binds only to addresses ResolveListen accepts, and resolves
// and binds every address independently: one bad address (e.g. Tailscale
// not installed, or a port already in use) never stops the others from
// serving. It errors only when zero addresses end up bound — the daemon and
// its workers must never be left with a completely dead dashboard/API
// because one optional address (typically "tailscale:PORT") wasn't
// available (CONTEXT D24).
func (s *Server) ListenAndServe(ctx context.Context, listen []string) error {
	result, err := ResolveListen(listen)
	if err != nil {
		return err
	}
	for _, skipped := range result.Skipped {
		if isTailscaleSentinel(skipped.Input) {
			s.log.Warn("httpapi: tailscale not available — dashboard reachable on 127.0.0.1 only",
				"addr", skipped.Input, "reason", skipped.Reason)
		} else {
			s.log.Warn("httpapi: dashboard.listen address skipped", "addr", skipped.Input, "reason", skipped.Reason)
		}
	}

	errCh := make(chan error, len(listen)+4) // headroom for later retry additions
	bound := 0
	for _, addr := range result.Addrs {
		if s.startListener(ctx, addr, errCh) {
			bound++
		}
	}
	if bound == 0 {
		s.shutdown()
		return fmt.Errorf("httpapi: no dashboard.listen address could be bound")
	}

	if result.NeedsTailscaleRetry() {
		go s.retryTailscale(ctx, listen, errCh)
	}

	select {
	case <-ctx.Done():
		s.shutdown()
		s.drain(errCh, bound)
		return ctx.Err()
	case err := <-errCh:
		s.shutdown()
		s.drain(errCh, bound-1)
		return err
	}
}

// startListener binds addr and starts serving on it in the background,
// reporting terminal errors on errCh. It returns false (logging a warning,
// never returning an error) when addr itself could not be bound, so the
// caller can keep any addresses that already bound successfully.
func (s *Server) startListener(ctx context.Context, addr string, errCh chan error) bool {
	s.mu.Lock()
	if s.boundAddrs[addr] {
		s.mu.Unlock()
		return false
	}
	s.mu.Unlock()

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		s.log.Warn("httpapi: bind failed, continuing with any other addresses", "addr", addr, "error", err)
		return false
	}
	srv := &http.Server{
		Handler:           s.handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		// WriteTimeout left unset so SSE streams can stay open.
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
	s.mu.Lock()
	if s.boundAddrs == nil {
		s.boundAddrs = map[string]bool{}
	}
	s.boundAddrs[addr] = true
	s.servers = append(s.servers, srv)
	s.listener = append(s.listener, ln)
	s.mu.Unlock()

	s.log.Info("httpapi listening", "addr", ln.Addr().String())
	go func() {
		err := srv.Serve(ln)
		if err != nil && err != http.ErrServerClosed {
			errCh <- err
			return
		}
		errCh <- nil
	}()
	return true
}

// retryTailscale periodically retries the Tailscale IP lookup so the
// dashboard picks up a Tailscale listener if it comes up after boot
// (e.g. `tailscale up` run later, or the service was still starting).
// It stops once a Tailscale address binds, or ctx is cancelled.
func (s *Server) retryTailscale(ctx context.Context, listen []string, errCh chan error) {
	const interval = 30 * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			result, err := ResolveListen(listen)
			if err != nil {
				continue
			}
			for _, addr := range result.Addrs {
				if !isTailscaleAddr(addr) {
					continue
				}
				if s.startListener(ctx, addr, errCh) {
					s.log.Info("httpapi: tailscale became available, dashboard listener added", "addr", addr)
					return
				}
			}
		}
	}
}

func isTailscaleAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	return err == nil && host != "127.0.0.1"
}

// drain reads up to n more results off errCh without blocking forever once
// the server set is already shutting down.
func (s *Server) drain(errCh chan error, n int) {
	for i := 0; i < n; i++ {
		<-errCh
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
