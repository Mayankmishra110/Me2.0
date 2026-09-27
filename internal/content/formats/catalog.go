// Package formats is the format engine (CONTENT_STRATEGY §3, COMPLIANCE G6).
package formats

// Kind is the video length class a format can serve.
type Kind string

const (
	KindLong  Kind = "long"
	KindShort Kind = "short"
)

// Info describes one structural format from CONTENT_STRATEGY §3.
type Info struct {
	ID      string
	Label   string
	Long    bool
	Short   bool
	BestFor []string // topic keywords for fit scoring
}

// Catalog is the full CONTENT_STRATEGY §3 list (ids match channels.go).
var Catalog = []Info{
	{ID: "explained_60s", Label: "Explained in 60 seconds", Short: true, BestFor: []string{"concept", "news", "explain", "what is", "meaning", "क्या", "समझ"}},
	{ID: "myth_vs_fact", Label: "Myth vs Fact", Long: true, Short: true, BestFor: []string{"myth", "fact", "hype", "false", "true", "misconception", "मिथक", "सच"}},
	{ID: "top_n", Label: "Top-N list", Long: true, Short: true, BestFor: []string{"top", "best", "list", "tools", "apps", "side hustle", "सबसे", "बेस्ट"}},
	{ID: "case_study", Label: "Case study / story", Long: true, Short: true, BestFor: []string{"story", "founder", "journey", "case", "business", "कैसे बना", "कहानी"}},
	{ID: "comparison", Label: "Comparison (A vs B)", Long: true, Short: true, BestFor: []string{"vs", "versus", "compare", "difference", "or", "तुलना", "या"}},
	{ID: "how_to", Label: "Step-by-step how-to", Long: true, Short: true, BestFor: []string{"how to", "steps", "tutorial", "guide", "workflow", "कैसे", "स्टेप"}},
	{ID: "news_breakdown", Label: "News breakdown", Long: true, Short: true, BestFor: []string{"news", "launch", "announce", "update", "breaking", "खबर", "लॉन्च"}},
	{ID: "mistakes_to_avoid", Label: "Mistakes to avoid", Long: true, Short: true, BestFor: []string{"mistake", "avoid", "wrong", "error", "don't", "गलती", "बचें"}},
	{ID: "timeline", Label: "Timeline / history of", Long: true, BestFor: []string{"history", "timeline", "evolution", "over time", "इतिहास"}},
	{ID: "qa", Label: "Q&A / viewer question", Long: true, Short: true, BestFor: []string{"question", "ask", "answer", "q&a", "faq", "सवाल", "जवाब"}},
}

var byID map[string]Info

func init() {
	byID = make(map[string]Info, len(Catalog))
	for _, f := range Catalog {
		byID[f.ID] = f
	}
}

// Lookup returns format info by id.
func Lookup(id string) (Info, bool) {
	f, ok := byID[id]
	return f, ok
}

// HookStyles are distinct opening styles for G6 variety.
var HookStyles = []string{
	"surprising_fact",
	"bold_promise",
	"question",
	"number_lead",
	"myth_callout",
	"story_open",
}
