package builder

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"mayank2/internal/config"
)

type fakeBuildsDB struct {
	prURL   string
	buildID string
	execErr error
}

func (f *fakeBuildsDB) ExecContext(_ context.Context, query string, args ...any) (sql.Result, error) {
	if f.execErr != nil {
		return nil, f.execErr
	}
	if strings.Contains(query, "UPDATE builds SET pr_url") {
		if len(args) >= 2 {
			f.prURL, _ = args[0].(string)
			f.buildID, _ = args[1].(string)
		}
	}
	return stubResult{}, nil
}

func (f *fakeBuildsDB) QueryRowContext(context.Context, string, ...any) *sql.Row {
	return nil
}

type stubResult struct{}

func (stubResult) LastInsertId() (int64, error) { return 0, nil }
func (stubResult) RowsAffected() (int64, error) { return 1, nil }

func TestPushPR(t *testing.T) {
	t.Parallel()

	const (
		repoName = "configured-repo" // never a real product name
		buildID  = "build-1"
		branch   = "build/phase-1"
	)

	baseReq := PushRequest{BuildID: buildID, Repo: repoName, Branch: branch}

	tests := []struct {
		name        string
		cfg         PRConfig
		req         PushRequest
		git         GitRunner
		wantErr     error
		wantNoGit   bool
		wantPRURL   string
		wantBuildID string
	}{
		{
			name: "disabled is clean no-op",
			cfg:  PRConfig{Enabled: false, Repos: []config.RepoConfig{{Name: repoName, Path: "/tmp/x"}}},
			req:  baseReq,
			git: func(context.Context, string, ...string) (string, error) {
				t.Fatal("git must not run when disabled")
				return "", nil
			},
			wantNoGit: true,
		},
		{
			name: "repo not in allowlist",
			cfg: PRConfig{
				Enabled: true,
				Repos:   []config.RepoConfig{{Name: "other", Path: "/tmp/x"}},
			},
			req:     baseReq,
			wantErr: ErrRepoNotAllowed,
			git: func(context.Context, string, ...string) (string, error) {
				t.Fatal("git must not run for disallowed repo")
				return "", nil
			},
		},
		{
			name: "refuses main branch",
			cfg: PRConfig{
				Enabled: true,
				Repos:   []config.RepoConfig{{Name: repoName, Path: "/tmp/x", BaseBranch: "main"}},
			},
			req:     PushRequest{BuildID: buildID, Repo: repoName, Branch: "main"},
			wantErr: ErrPushMainForbidden,
			git: func(context.Context, string, ...string) (string, error) {
				t.Fatal("git must not run when targeting main")
				return "", nil
			},
		},
		{
			name: "refuses master branch",
			cfg: PRConfig{
				Enabled: true,
				Repos:   []config.RepoConfig{{Name: repoName, Path: "/tmp/x"}},
			},
			req:     PushRequest{BuildID: buildID, Repo: repoName, Branch: "master"},
			wantErr: ErrPushMainForbidden,
			git: func(context.Context, string, ...string) (string, error) {
				t.Fatal("git must not run when targeting master")
				return "", nil
			},
		},
		{
			name: "empty branch forbidden",
			cfg: PRConfig{
				Enabled: true,
				Repos:   []config.RepoConfig{{Name: repoName, Path: "/tmp/x"}},
			},
			req:     PushRequest{BuildID: buildID, Repo: repoName, Branch: "  "},
			wantErr: ErrPushMainForbidden,
			git: func(context.Context, string, ...string) (string, error) {
				t.Fatal("git must not run for empty branch")
				return "", nil
			},
		},
		{
			name: "push + compare link recorded",
			cfg: PRConfig{
				Enabled: true,
				Repos: []config.RepoConfig{{
					Name:       repoName,
					Path:       "/tmp/configured",
					BaseBranch: "main",
				}},
			},
			req: baseReq,
			git: func(_ context.Context, dir string, args ...string) (string, error) {
				if dir != "/tmp/configured" {
					t.Fatalf("dir=%q", dir)
				}
				switch {
				case len(args) == 3 && args[0] == "push" && args[1] == "origin" && args[2] == branch:
					if args[2] == "main" || containsForce(args) {
						t.Fatalf("forbidden push argv: %v", args)
					}
					return "", nil
				case len(args) == 3 && args[0] == "remote" && args[1] == "get-url" && args[2] == "origin":
					return "https://github.com/acme/widget.git\n", nil
				default:
					t.Fatalf("unexpected git argv: %v", args)
					return "", nil
				}
			},
			wantPRURL:   "https://github.com/acme/widget/compare/main...build%2Fphase-1",
			wantBuildID: buildID,
		},
		{
			name: "ssh remote compare link",
			cfg: PRConfig{
				Enabled: true,
				Repos: []config.RepoConfig{{
					Name:       repoName,
					Path:       "/tmp/configured",
					BaseBranch: "develop",
				}},
			},
			req: baseReq,
			git: func(_ context.Context, _ string, args ...string) (string, error) {
				switch {
				case args[0] == "push":
					return "", nil
				case args[0] == "remote":
					return "git@github.com:acme/widget.git", nil
				default:
					t.Fatalf("unexpected git argv: %v", args)
					return "", nil
				}
			},
			wantPRURL:   "https://github.com/acme/widget/compare/develop...build%2Fphase-1",
			wantBuildID: buildID,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			db := &fakeBuildsDB{}
			p := &Pusher{Config: tt.cfg, DB: db, Git: tt.git}
			err := p.PushPR(context.Background(), tt.req)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err=%v want %v", err, tt.wantErr)
				}
				if db.prURL != "" {
					t.Fatalf("pr_url should stay empty on error, got %q", db.prURL)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if tt.wantPRURL != "" && db.prURL != tt.wantPRURL {
				t.Fatalf("pr_url=%q want %q", db.prURL, tt.wantPRURL)
			}
			if tt.wantBuildID != "" && db.buildID != tt.wantBuildID {
				t.Fatalf("build id=%q want %q", db.buildID, tt.wantBuildID)
			}
			if tt.wantNoGit && db.prURL != "" {
				t.Fatalf("disabled path should not set pr_url")
			}
		})
	}
}

func containsForce(args []string) bool {
	for _, a := range args {
		if a == "--force" || a == "-f" {
			return true
		}
	}
	return false
}

func TestCompareURLRejectsBadRemote(t *testing.T) {
	t.Parallel()
	_, err := compareURL("not-a-remote", "main", "feat")
	if err == nil {
		t.Fatal("expected error")
	}
}
