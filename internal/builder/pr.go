// PushPR (M2-505): after the last subphase is gated pass (M2-504), push the
// build branch (never main) and record a compare-link pr_url on the builds row.
//
// PR mechanism (ticket Notes): plain GitHub compare URL from `git remote
// get-url origin` — no gh CLI, no GitHub API token. Mayank can upgrade later.
package builder

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os/exec"
	"strings"

	"mayank2/internal/config"
)

var (
	// ErrBuilderDisabled is returned when config.builder.enabled is false
	// and the caller asked for a hard failure; RunPushPR treats disabled as a
	// clean no-op instead (same gate shape as planner/implementer).
	ErrBuilderDisabled = errors.New("builder: disabled")
	// ErrRepoNotAllowed means the repo name is not in config.builder.repos.
	ErrRepoNotAllowed = errors.New("builder: repo not in builder.repos")
	// ErrPushMainForbidden is returned when the branch to push is main/master
	// or empty — Builder never pushes to main (CLAUDE.md, ARCHITECTURE §7).
	ErrPushMainForbidden = errors.New("builder: refusing to push main")
)

// BuildsDB is the SQL surface PushPR needs (*sql.DB satisfies it).
type BuildsDB interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// GitRunner runs fixed-argv git commands (injectable for tests).
type GitRunner func(ctx context.Context, dir string, args ...string) (stdout string, err error)

// Notifier is optional Telegram delivery of the PR/compare link.
type Notifier func(ctx context.Context, repo, branch, prURL string) error

// PRConfig is the Builder subset PushPR needs.
type PRConfig struct {
	Enabled bool
	Repos   []config.RepoConfig
}

func (c PRConfig) findRepo(name string) (config.RepoConfig, bool) {
	name = strings.TrimSpace(name)
	for _, r := range c.Repos {
		if r.Name == name {
			return r, true
		}
	}
	return config.RepoConfig{}, false
}

// PushRequest is one completed plan's push + compare-link step.
type PushRequest struct {
	BuildID string
	Repo    string // config.builder.repos[].name — never a hardcoded product repo
	Branch  string
}

// Pusher pushes a build branch and records pr_url.
type Pusher struct {
	Config PRConfig
	DB     BuildsDB
	Git    GitRunner
	Notify Notifier
	Logger *slog.Logger
}

func (p *Pusher) logger() *slog.Logger {
	if p.Logger != nil {
		return p.Logger
	}
	return slog.Default()
}

func defaultGitRunner(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %v in %s: %w\n%s", args, dir, err, out)
	}
	return string(out), nil
}

// PushPR no-ops when builder is disabled. Otherwise validates the repo allowlist,
// refuses main, pushes origin <branch> (never --force), writes a compare-link
// pr_url onto the builds row, and optionally notifies Telegram.
func (p *Pusher) PushPR(ctx context.Context, req PushRequest) error {
	if !p.Config.Enabled {
		p.logger().Info("builder.pr skipped: builder.enabled=false")
		return nil
	}
	if p.DB == nil {
		return fmt.Errorf("builder.pr: db is required")
	}
	git := p.Git
	if git == nil {
		git = defaultGitRunner
	}

	repo, ok := p.Config.findRepo(req.Repo)
	if !ok {
		return fmt.Errorf("%w: %q", ErrRepoNotAllowed, req.Repo)
	}
	if strings.TrimSpace(repo.Path) == "" {
		return fmt.Errorf("builder.pr: repo %q has empty path", req.Repo)
	}

	branch := strings.TrimSpace(req.Branch)
	if branch == "" || isMainBranch(branch) {
		return fmt.Errorf("%w: branch=%q", ErrPushMainForbidden, branch)
	}
	if strings.TrimSpace(req.BuildID) == "" {
		return fmt.Errorf("builder.pr: build id is required")
	}

	base := strings.TrimSpace(repo.BaseBranch)
	if base == "" {
		base = "main"
	}

	// Fixed argv only — never shell, never --force, never main as the push ref.
	if _, err := git(ctx, repo.Path, "push", "origin", branch); err != nil {
		return fmt.Errorf("builder.pr push %s: %w", branch, err)
	}

	remoteOut, err := git(ctx, repo.Path, "remote", "get-url", "origin")
	if err != nil {
		return fmt.Errorf("builder.pr remote url: %w", err)
	}
	prURL, err := compareURL(strings.TrimSpace(remoteOut), base, branch)
	if err != nil {
		return fmt.Errorf("builder.pr compare url: %w", err)
	}

	if _, err := p.DB.ExecContext(ctx,
		`UPDATE builds SET pr_url = ? WHERE id = ?`,
		prURL, req.BuildID,
	); err != nil {
		return fmt.Errorf("builder.pr update builds %s: %w", req.BuildID, err)
	}

	if p.Notify != nil {
		if err := p.Notify(ctx, req.Repo, branch, prURL); err != nil {
			return fmt.Errorf("builder.pr notify: %w", err)
		}
	}

	p.logger().Info("builder.pr recorded compare link",
		"build_id", req.BuildID,
		"repo", req.Repo,
		"branch", branch,
		"pr_url", prURL,
	)
	return nil
}

func isMainBranch(branch string) bool {
	b := strings.ToLower(strings.TrimSpace(branch))
	return b == "main" || b == "master"
}

// compareURL builds https://github.com/<owner>/<repo>/compare/<base>...<branch>
// from a git remote URL (https or ssh). Non-GitHub remotes get a best-effort
// https host rewrite when the path looks like owner/repo.git.
func compareURL(remote, base, branch string) (string, error) {
	owner, repo, err := parseGitHubRemote(remote)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"https://github.com/%s/%s/compare/%s...%s",
		url.PathEscape(owner),
		url.PathEscape(repo),
		url.PathEscape(base),
		url.PathEscape(branch),
	), nil
}

func parseGitHubRemote(remote string) (owner, repo string, err error) {
	remote = strings.TrimSpace(remote)
	if remote == "" {
		return "", "", fmt.Errorf("empty remote url")
	}

	// git@github.com:owner/repo.git
	if strings.HasPrefix(remote, "git@") {
		_, after, ok := strings.Cut(remote, ":")
		if !ok {
			return "", "", fmt.Errorf("ssh remote missing path: %s", remote)
		}
		return splitOwnerRepo(after)
	}

	u, err := url.Parse(remote)
	if err != nil {
		return "", "", fmt.Errorf("parse remote: %w", err)
	}
	path := strings.TrimPrefix(u.Path, "/")
	return splitOwnerRepo(path)
}

func splitOwnerRepo(path string) (owner, repo string, err error) {
	path = strings.TrimSuffix(strings.TrimSpace(path), ".git")
	parts := strings.Split(path, "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("remote path not owner/repo: %q", path)
	}
	return parts[0], parts[1], nil
}
