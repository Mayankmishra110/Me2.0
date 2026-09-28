package content

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"mayank2/internal/llm"
	"mayank2/internal/queue"
)

const (
	JobAffiliatePin = "affiliate.pin"

	FormatAffiliatePin = "affiliate_pin"
	DefaultDisclosure  = "#ad This pin contains affiliate links."
)

// AffiliateCompleter is the free-tier LLM surface (TaskMetadata / TaskScript).
type AffiliateCompleter interface {
	Complete(ctx context.Context, task llm.Task, req llm.Request) (llm.Response, error)
}

// AffiliateApprovals starts D5 approval before publish.pinterest.
type AffiliateApprovals interface {
	Start(ctx context.Context, contentID, kind, summary, previewPath string) (string, error)
}

// AffiliateEnqueuer enqueues publish.pinterest after approval (callers only).
type AffiliateEnqueuer interface {
	Enqueue(ctx context.Context, jobType string, payload any, opts ...queue.EnqueueOpt) (id string, err error)
}

// AffiliateConfig is loaded from config/affiliate/programs.yaml.
type AffiliateConfig struct {
	Programs []struct {
		ID         string `yaml:"id"`
		Name       string `yaml:"name"`
		Disclosure string `yaml:"disclosure"`
	} `yaml:"programs"`
	Boards []struct {
		ID      string `yaml:"id"`
		Name    string `yaml:"name"`
		Program string `yaml:"program"`
	} `yaml:"boards"`
}

// LoadAffiliateConfig reads programs.yaml.
func LoadAffiliateConfig(path string) (AffiliateConfig, error) {
	var c AffiliateConfig
	b, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	if err := yaml.Unmarshal(b, &c); err != nil {
		return c, err
	}
	return c, nil
}

// AffiliatePinRequest is one candidate pin.
type AffiliatePinRequest struct {
	ProductTitle string
	Destination  string // plain affiliate URL — never rewritten
	BoardID      string
	ProgramID    string
	ChannelID    string
	RedoNotes    string
}

// AffiliatePinResult is the generated candidate ready for approval.
type AffiliatePinResult struct {
	ContentID   string
	Title       string
	Description string
	Link        string // unmodified destination
	BoardID     string
	ApprovalID  string
}

// AffiliatePins selects/generates affiliate Idea pin copy upstream of M2-304.
type AffiliatePins struct {
	DB        *sql.DB
	LLM       AffiliateCompleter
	Approvals AffiliateApprovals
	Cfg       AffiliateConfig
	Now       func() time.Time
	NewID     func() string
	// UsedDestinations returns destination URLs already used today (selection primary control).
	UsedDestinations func(ctx context.Context, day string) (map[string]struct{}, error)
}

func (a *AffiliatePins) now() time.Time {
	if a != nil && a.Now != nil {
		return a.Now().UTC()
	}
	return time.Now().UTC()
}

func (a *AffiliatePins) id() string {
	if a != nil && a.NewID != nil {
		return a.NewID()
	}
	return fmt.Sprintf("pin%d", a.now().UnixNano())
}

// Prepare builds pin copy with FTC disclosure, refuses cloaking, skips same-day
// duplicate destinations, and creates approval.request. Does not call publish.pinterest.
func (a *AffiliatePins) Prepare(ctx context.Context, req AffiliatePinRequest) (AffiliatePinResult, error) {
	var zero AffiliatePinResult
	if a == nil || a.DB == nil || a.LLM == nil {
		return zero, errors.New("affiliate: nil service")
	}
	link := strings.TrimSpace(req.Destination)
	if link == "" {
		return zero, errors.New("affiliate: destination required")
	}
	if err := assertPlainURL(link); err != nil {
		return zero, err
	}
	day := a.now().Format("2006-01-02")
	if a.UsedDestinations != nil {
		used, err := a.UsedDestinations(ctx, day)
		if err != nil {
			return zero, err
		}
		if _, ok := used[link]; ok {
			return zero, fmt.Errorf("affiliate: destination already used today (%s) — skip", day)
		}
	} else if err := a.defaultUsedCheck(ctx, link, day); err != nil {
		return zero, err
	}

	disclosure := DefaultDisclosure
	for _, p := range a.Cfg.Programs {
		if p.ID == req.ProgramID && strings.TrimSpace(p.Disclosure) != "" {
			disclosure = p.Disclosure
			break
		}
	}

	resp, err := a.LLM.Complete(ctx, llm.TaskMetadata, llm.Request{
		System: "Write a short Pinterest Idea pin title and description. Free-tier content only.",
		Messages: []llm.Message{{Role: "user", Content: fmt.Sprintf(
			"Product: %s\nLink: %s\nRedo: %s\nReturn JSON {\"title\",\"description\"}.",
			req.ProductTitle, link, req.RedoNotes)}},
	})
	if err != nil {
		return zero, err
	}
	title, desc := parsePinCopy(resp.Text, req.ProductTitle)
	if !strings.Contains(desc, disclosure) && !strings.Contains(desc, "#ad") {
		desc = strings.TrimSpace(desc) + "\n\n" + disclosure
	}

	// Pass link through unmodified — no cloaking / internal redirect.
	finalLink := link

	cid := a.id()
	channel := req.ChannelID
	if channel == "" {
		channel = "yt-money-en"
	}
	if err := a.ensureChannel(ctx, channel); err != nil {
		return zero, err
	}
	script, _ := json.Marshal(map[string]string{
		"title": title, "description": desc, "link": finalLink, "board_id": req.BoardID,
	})
	_, err = a.DB.ExecContext(ctx, `
INSERT INTO content_items (id, channel_id, kind, format, language, stage, script, created_at)
VALUES (?, ?, 'short', ?, 'en', 'approval', ?, ?)`,
		cid, channel, FormatAffiliatePin, string(script), a.now().Format(time.RFC3339Nano))
	if err != nil {
		return zero, err
	}

	var approvalID string
	if a.Approvals != nil {
		approvalID, err = a.Approvals.Start(ctx, cid, "short",
			fmt.Sprintf("Pinterest affiliate pin: %s", title), "")
		if err != nil {
			return zero, err
		}
	}
	return AffiliatePinResult{
		ContentID: cid, Title: title, Description: desc,
		Link: finalLink, BoardID: req.BoardID, ApprovalID: approvalID,
	}, nil
}

func (a *AffiliatePins) defaultUsedCheck(ctx context.Context, link, day string) error {
	var n int
	err := a.DB.QueryRowContext(ctx, `
SELECT COUNT(*) FROM content_items
WHERE format=? AND date(created_at)=? AND script LIKE ?`,
		FormatAffiliatePin, day, "%"+link+"%").Scan(&n)
	if err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("affiliate: destination already used today (%s) — skip", day)
	}
	return nil
}

func (a *AffiliatePins) ensureChannel(ctx context.Context, id string) error {
	_, err := a.DB.ExecContext(ctx, `
INSERT OR IGNORE INTO channels (id, platform, handle, language, niche, account_ref, status)
VALUES (?, 'youtube', '', 'en', 'money_side_hustles', '', 'active')`, id)
	return err
}

func assertPlainURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("affiliate: destination must be a plain http(s) URL")
	}
	// Refuse known cloakers / shorteners as a soft guard (M2-304 is the hard backstop).
	host := strings.ToLower(u.Host)
	for _, bad := range []string{"bit.ly", "t.co", "tinyurl.com", "go.mayank2.local"} {
		if host == bad || strings.HasSuffix(host, "."+bad) {
			return fmt.Errorf("affiliate: cloaked/shortened URLs are not allowed (%s)", host)
		}
	}
	return nil
}

func parsePinCopy(text, fallbackTitle string) (title, desc string) {
	text = strings.TrimSpace(text)
	var parsed struct {
		Title       string `json:"title"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal([]byte(text), &parsed); err == nil && parsed.Title != "" {
		return parsed.Title, parsed.Description
	}
	// fenced json
	if i := strings.Index(text, "{"); i >= 0 {
		if j := strings.LastIndex(text, "}"); j > i {
			if err := json.Unmarshal([]byte(text[i:j+1]), &parsed); err == nil && parsed.Title != "" {
				return parsed.Title, parsed.Description
			}
		}
	}
	return fallbackTitle, text
}

// DefaultAffiliateConfigPath returns config/affiliate/programs.yaml under root.
func DefaultAffiliateConfigPath(root string) string {
	return filepath.Join(root, "config", "affiliate", "programs.yaml")
}
