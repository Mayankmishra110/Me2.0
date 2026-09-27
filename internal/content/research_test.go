package content

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mayank2/internal/llm"
)

type fakeCompleter struct {
	resp llm.Response
	err  error
	last llm.Request
}

func (f *fakeCompleter) Complete(_ context.Context, task llm.Task, req llm.Request) (llm.Response, error) {
	if task != llm.TaskResearch {
		return llm.Response{}, errors.New("unexpected task")
	}
	f.last = req
	if f.err != nil {
		return llm.Response{}, f.err
	}
	return f.resp, nil
}

func mustRead(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestExtractReadableText_stripsScriptStyle(t *testing.T) {
	html := string(mustRead(t, "research_page_ok.html"))
	text := extractReadableText(html)
	if strings.Contains(text, "alert") || strings.Contains(text, "color: red") {
		t.Fatalf("script/style leaked: %q", text)
	}
	if !strings.Contains(text, "Freelance income") || !strings.Contains(text, "$52,000") {
		t.Fatalf("missing body text: %q", text)
	}
	if strings.Contains(text, "<") {
		t.Fatalf("tags remain: %q", text)
	}
}

func TestRobotsPathAllowed(t *testing.T) {
	robots := string(mustRead(t, "research_robots.txt"))
	cases := []struct {
		path string
		want bool
	}{
		{"/", true},
		{"/article", true},
		{"/private/secret", false},
		{"/private/", false},
		{"/private", true}, // Disallow is /private/ (trailing slash)
	}
	for _, tc := range cases {
		got := robotsPathAllowed(robots, tc.path)
		if got != tc.want {
			t.Errorf("path %q: got %v want %v", tc.path, got, tc.want)
		}
	}
}

func TestResearchRun_fixturePages(t *testing.T) {
	pageOK := mustRead(t, "research_page_ok.html")
	pageAI := mustRead(t, "research_page_ai.html")
	robots := mustRead(t, "research_robots.txt")

	mux := http.NewServeMux()
	mux.HandleFunc("/robots.txt", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(robots)
	})
	mux.HandleFunc("/ok", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write(pageOK)
	})
	mux.HandleFunc("/ai", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write(pageAI)
	})
	mux.HandleFunc("/private/secret", func(w http.ResponseWriter, r *http.Request) {
		t.Error("robots-disallowed path was fetched")
		http.Error(w, "forbidden", http.StatusForbidden)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	briefJSON := `{
  "angle": "Show realistic freelance income with sourced numbers, not hype.",
  "facts": [
    {
      "claim": "Average US freelancer earned $52,000 in 2025.",
      "sources": ["` + srv.URL + `/ok"]
    },
    {
      "claim": "ChatGPT Plus costs $20 per month.",
      "sources": ["` + srv.URL + `/ai"]
    }
  ],
  "key_numbers": [
    {"label": "avg freelancer income", "value": "$52,000", "source": "` + srv.URL + `/ok"},
    {"label": "ChatGPT Plus", "value": "$20/mo", "source": "` + srv.URL + `/ai"}
  ],
  "open_questions": ["How does income vary by niche?"]
}`
	fc := &fakeCompleter{resp: llm.Response{Text: briefJSON, Provider: "fake", Model: "test"}}

	outDir := t.TempDir()
	r, err := NewResearch(ResearchOptions{
		HTTPClient: srv.Client(),
		MaxSources: 3,
		OutDir:     outDir,
		Completer:  fc,
	})
	if err != nil {
		t.Fatal(err)
	}

	brief, err := r.Run(context.Background(), ResearchInput{
		Topic: "Freelance income vs AI tool costs",
		URLs: []string{
			srv.URL + "/ok",
			srv.URL + "/ai",
			srv.URL + "/private/secret", // robots block
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if brief.Angle == "" || len(brief.Facts) < 1 {
		t.Fatalf("brief incomplete: %+v", brief)
	}
	for i, f := range brief.Facts {
		if len(f.Sources) < 1 {
			t.Fatalf("fact %d missing sources", i)
		}
	}
	if len(brief.Sources) != 2 {
		t.Fatalf("stored sources=%d want 2 (private blocked)", len(brief.Sources))
	}

	// brief.json on disk
	raw, err := os.ReadFile(filepath.Join(outDir, "brief.json"))
	if err != nil {
		t.Fatal(err)
	}
	var disk Brief
	if err := json.Unmarshal(raw, &disk); err != nil {
		t.Fatal(err)
	}
	if disk.Angle != brief.Angle {
		t.Fatalf("disk angle mismatch")
	}

	// source texts stored for originality gate
	for _, src := range brief.Sources {
		p := filepath.Join(outDir, filepath.FromSlash(src.Path))
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("source file %s: %v", src.Path, err)
		}
		if len(b) == 0 || src.Bytes != len(b) {
			t.Fatalf("source bytes mismatch path=%s", src.Path)
		}
		if src.Kind != "page" {
			t.Fatalf("kind=%s", src.Kind)
		}
		text := string(b)
		if strings.Contains(text, "<script") || strings.Contains(text, "alert(") {
			t.Fatalf("stored HTML not extracted: %q", text[:min(80, len(text))])
		}
	}

	if !strings.Contains(fc.last.Messages[0].Content, "Freelance income") {
		t.Fatalf("LLM prompt missing page text")
	}
	if len(fc.last.JSONSchema) == 0 {
		t.Fatal("expected JSON schema on LLM request")
	}
}

func TestResearchRun_videoTranscript(t *testing.T) {
	outDir := t.TempDir()
	videoURL := "https://www.youtube.com/watch?v=dQw4w9WgXcQ"
	transcript := "The creator says freelancers average fifty two thousand dollars. Always cite sources."

	briefJSON := `{
  "angle": "Use competitor video as notes only.",
  "facts": [
    {"claim": "A referenced video mentions $52k freelancer average.", "sources": ["` + videoURL + `"]}
  ],
  "key_numbers": [
    {"label": "mentioned income", "value": "$52k", "source": "` + videoURL + `"}
  ],
  "open_questions": ["Verify the figure against primary data."]
}`
	fc := &fakeCompleter{resp: llm.Response{Text: briefJSON, Provider: "fake", Model: "test"}}

	var gotURL string
	r, err := NewResearch(ResearchOptions{
		HTTPClient: http.DefaultClient,
		OutDir:     outDir,
		Completer:  fc,
		FetchTranscript: func(_ context.Context, u string) (string, error) {
			gotURL = u
			return transcript, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	brief, err := r.Run(context.Background(), ResearchInput{
		Topic:    "Side hustle research from a video",
		VideoURL: videoURL,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if gotURL != videoURL {
		t.Fatalf("transcript URL=%q", gotURL)
	}
	if len(brief.Sources) != 1 || brief.Sources[0].Kind != "transcript" {
		t.Fatalf("sources=%+v", brief.Sources)
	}
	p := filepath.Join(outDir, filepath.FromSlash(brief.Sources[0].Path))
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != transcript {
		t.Fatalf("stored transcript mismatch")
	}
}

func TestResearchRun_maxSources(t *testing.T) {
	robots := []byte("User-agent: *\nAllow: /\n")
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			_, _ = w.Write(robots)
			return
		}
		n++
		_, _ = io.WriteString(w, "<html><body><p>Page number content here.</p></body></html>")
	}))
	defer srv.Close()

	fc := &fakeCompleter{resp: llm.Response{Text: `{
  "angle": "cap sources",
  "facts": [{"claim": "something", "sources": ["` + srv.URL + `/a"]}],
  "key_numbers": [],
  "open_questions": []
}`}}

	outDir := t.TempDir()
	r, err := NewResearch(ResearchOptions{
		HTTPClient: srv.Client(),
		MaxSources: 2,
		OutDir:     outDir,
		Completer:  fc,
	})
	if err != nil {
		t.Fatal(err)
	}
	brief, err := r.Run(context.Background(), ResearchInput{
		Topic: "cap",
		URLs:  []string{srv.URL + "/a", srv.URL + "/b", srv.URL + "/c"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(brief.Sources) != 2 {
		t.Fatalf("sources=%d want 2", len(brief.Sources))
	}
	if n != 2 {
		t.Fatalf("page fetches=%d want 2", n)
	}
}

func TestResearchRun_noSources(t *testing.T) {
	robots := []byte("User-agent: *\nDisallow: /\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			_, _ = w.Write(robots)
			return
		}
		t.Error("should not fetch when Disallow: /")
	}))
	defer srv.Close()

	r, err := NewResearch(ResearchOptions{
		HTTPClient: srv.Client(),
		OutDir:     t.TempDir(),
		Completer:  &fakeCompleter{resp: llm.Response{Text: `{}`}},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.Run(context.Background(), ResearchInput{
		Topic: "blocked",
		URLs:  []string{srv.URL + "/x"},
	})
	if !errors.Is(err, ErrResearchNoSources) {
		t.Fatalf("err=%v", err)
	}
}

func TestValidateBrief_requiresFactSource(t *testing.T) {
	err := validateBrief(&Brief{
		Angle: "x",
		Facts: []Fact{{Claim: "y", Sources: nil}},
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestIsVideoURL(t *testing.T) {
	cases := []struct {
		u    string
		want bool
	}{
		{"https://www.youtube.com/watch?v=abc", true},
		{"https://youtu.be/abc", true},
		{"https://vimeo.com/123", true},
		{"https://example.com/article", false},
	}
	for _, tc := range cases {
		if got := isVideoURL(tc.u); got != tc.want {
			t.Errorf("%s: got %v want %v", tc.u, got, tc.want)
		}
	}
}

func TestNewResearch_requiresFields(t *testing.T) {
	if _, err := NewResearch(ResearchOptions{}); err == nil {
		t.Fatal("expected error")
	}
	if _, err := NewResearch(ResearchOptions{Completer: &fakeCompleter{}}); err == nil {
		t.Fatal("expected OutDir error")
	}
}
