// Package tickets reads and updates markdown tickets with YAML frontmatter:
//
//	---
//	id: LAYIN-012
//	title: Export resume as PDF
//	status: ready
//	spec: docs/specs/resume-export.md
//	priority: 1
//	---
//	## Goal ...
package tickets

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const StatusReady = "ready"

type Ticket struct {
	ID       string `yaml:"id"`
	Title    string `yaml:"title"`
	Status   string `yaml:"status"`
	Spec     string `yaml:"spec"`
	Priority int    `yaml:"priority"` // 1 is most urgent; 0 means unset

	Path    string `yaml:"-"`
	Content string `yaml:"-"` // whole file, frontmatter included
}

var errNoFrontmatter = errors.New("no frontmatter")

func splitFrontmatter(content string) (string, error) {
	s := strings.ReplaceAll(content, "\r\n", "\n")
	if !strings.HasPrefix(s, "---\n") {
		return "", errNoFrontmatter
	}
	end := strings.Index(s[4:], "\n---")
	if end < 0 {
		return "", errNoFrontmatter
	}
	return s[4 : 4+end], nil
}

func Parse(path string) (Ticket, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Ticket{}, err
	}
	fm, err := splitFrontmatter(string(raw))
	if err != nil {
		return Ticket{}, fmt.Errorf("%s: %w", path, err)
	}
	var t Ticket
	if err := yaml.Unmarshal([]byte(fm), &t); err != nil {
		return Ticket{}, fmt.Errorf("%s: %w", path, err)
	}
	if t.ID == "" {
		return Ticket{}, fmt.Errorf("%s: frontmatter has no id", path)
	}
	t.Status = strings.ToLower(strings.TrimSpace(t.Status))
	t.Path = path
	t.Content = string(raw)
	return t, nil
}

// Scan returns every ticket in dir, most urgent first. Files that fail to parse
// are returned as errors alongside the good tickets, so one bad file never
// hides the rest.
func Scan(dir string) ([]Ticket, []error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, []error{err}
	}
	var out []Ticket
	var errs []error
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".md") || isDocFile(name) {
			continue
		}
		t, err := Parse(filepath.Join(dir, name))
		if err != nil {
			errs = append(errs, err)
			continue
		}
		out = append(out, t)
	}
	sort.SliceStable(out, func(i, j int) bool {
		pi, pj := rank(out[i].Priority), rank(out[j].Priority)
		if pi != pj {
			return pi < pj
		}
		return out[i].ID < out[j].ID
	})
	return out, errs
}

func isDocFile(name string) bool {
	switch strings.ToUpper(name) {
	case "README.MD", "TEMPLATE.MD":
		return true
	}
	return false
}

func rank(p int) int {
	if p <= 0 {
		return 1 << 30
	}
	return p
}

var statusLine = regexp.MustCompile(`(?m)^status:[^\r\n]*`)

// SetStatus rewrites the status line inside the frontmatter, leaving the rest
// of the file byte-for-byte unchanged.
func SetStatus(path, status string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	content := string(raw)
	if _, err := splitFrontmatter(content); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	// The frontmatter ends at the first "\n---" after the opening fence; only
	// search up to there so a "status:" line in the body is never touched.
	limit := strings.Index(content[3:], "\n---") + 3
	loc := statusLine.FindStringIndex(content[:limit])
	if loc == nil {
		return fmt.Errorf("%s: frontmatter has no status line", path)
	}
	updated := content[:loc[0]] + "status: " + status + content[loc[1]:]
	return os.WriteFile(path, []byte(updated), 0o644)
}
