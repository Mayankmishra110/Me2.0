// Package content holds the content-pipeline domain types, one file per
// stage. This file is the channel/format config stage (M2-201): it loads and
// validates config/channels/*.yaml into typed structs and syncs them into
// the `channels` table.
package content

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"mayank2/internal/config"
)

// Language is a channel's script/voice language. Only English and Hindi are
// supported today (CONTEXT.md D10).
type Language string

const (
	LanguageEN Language = "en"
	LanguageHI Language = "hi"
)

var validLanguages = map[Language]bool{
	LanguageEN: true,
	LanguageHI: true,
}

// CaptionStyle is how burned-in captions render (DESIGN §3).
type CaptionStyle string

const (
	CaptionWordHighlight CaptionStyle = "word-highlight"
	CaptionKaraoke       CaptionStyle = "karaoke"
	CaptionBlock         CaptionStyle = "block"
)

var validCaptionStyles = map[CaptionStyle]bool{
	CaptionWordHighlight: true,
	CaptionKaraoke:       true,
	CaptionBlock:         true,
}

// Format is a content format id (CONTENT_STRATEGY §3). The catalog here is
// the full structural list from that table; a channel may reference a format
// before its Remotion composition exists (compositions ship incrementally).
const (
	FormatExplained60s    = "explained_60s"
	FormatMythVsFact      = "myth_vs_fact"
	FormatTopN            = "top_n"
	FormatCaseStudy       = "case_study"
	FormatComparison      = "comparison"
	FormatHowTo           = "how_to"
	FormatNewsBreakdown   = "news_breakdown"
	FormatMistakesToAvoid = "mistakes_to_avoid"
	FormatTimeline        = "timeline"
	FormatQA              = "qa"
)

var validFormats = map[string]bool{
	FormatExplained60s:    true,
	FormatMythVsFact:      true,
	FormatTopN:            true,
	FormatCaseStudy:       true,
	FormatComparison:      true,
	FormatHowTo:           true,
	FormatNewsBreakdown:   true,
	FormatMistakesToAvoid: true,
	FormatTimeline:        true,
	FormatQA:              true,
}

// BrandKit matches remotion/src/types.ts's BrandKit field-for-field (json
// tags use its camelCase so internal/media can pass this straight through as
// composition props).
type BrandKit struct {
	Primary      string       `json:"primary"`
	Secondary    string       `json:"secondary"`
	FontHeading  string       `json:"fontHeading"`
	FontBody     string       `json:"fontBody"`
	CaptionStyle CaptionStyle `json:"captionStyle"`
}

// Voice is the TTS configuration for a channel.
type Voice struct {
	Engine  string  `json:"engine"`
	VoiceID string  `json:"voiceId"`
	Speed   float64 `json:"speed"`
}

// Windows is a channel's posting-time configuration.
type Windows struct {
	TZ    string   `json:"tz"`
	Slots []string `json:"slots"`
}

// Channel is a validated config/channels/<id>.yaml entry (DESIGN §3 schema).
type Channel struct {
	ID         string
	Platform   string
	Handle     string
	Language   Language
	Niche      string
	Brand      BrandKit
	Voice      Voice
	Formats    []string
	Windows    Windows
	Disclaimer *string
	SourceFile string
}

// LoadChannels reads every *.yaml/*.yml file in dir, validates it, and
// returns the channels sorted by id. Every error names the source file and
// the field that failed.
func LoadChannels(dir string) ([]Channel, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read channels dir %s: %w", dir, err)
	}

	var channels []Channel
	bySourceFile := map[string]string{} // id -> source file, for duplicate errors
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
			continue
		}
		path := filepath.Join(dir, name)

		cfgCh, err := config.LoadChannel(path)
		if err != nil {
			return nil, err // already wrapped with the path by internal/config
		}

		ch, err := fromConfigChannel(*cfgCh)
		if err != nil {
			return nil, err
		}

		if prev, ok := bySourceFile[ch.ID]; ok {
			return nil, fmt.Errorf("%s: duplicate channel id %q (also defined in %s)", path, ch.ID, prev)
		}
		bySourceFile[ch.ID] = path

		channels = append(channels, ch)
	}

	sort.Slice(channels, func(i, j int) bool { return channels[i].ID < channels[j].ID })
	return channels, nil
}

// fromConfigChannel converts an internal/config.Channel (already
// shape-validated) into a content.Channel, applying this package's stricter
// domain rules: language enum, format catalog, caption-style enum.
func fromConfigChannel(c config.Channel) (Channel, error) {
	lang := Language(c.Language)
	if !validLanguages[lang] {
		return Channel{}, fmt.Errorf("%s: language: %q is not supported (want en or hi)", c.SourceFile, c.Language)
	}

	if len(c.Formats) == 0 {
		// internal/config already rejects this, but guard defensively.
		return Channel{}, fmt.Errorf("%s: formats: must list at least one format", c.SourceFile)
	}
	for i, f := range c.Formats {
		if !validFormats[f] {
			return Channel{}, fmt.Errorf("%s: formats[%d]: unknown format id %q", c.SourceFile, i, f)
		}
	}

	captionStyle := CaptionStyle(c.Brand.CaptionStyle)
	if !validCaptionStyles[captionStyle] {
		return Channel{}, fmt.Errorf("%s: brand.caption_style: %q is not one of word-highlight, karaoke, block", c.SourceFile, c.Brand.CaptionStyle)
	}
	if c.Brand.FontHeading == "" {
		return Channel{}, fmt.Errorf("%s: brand.font_heading is required", c.SourceFile)
	}
	if c.Brand.FontBody == "" {
		return Channel{}, fmt.Errorf("%s: brand.font_body is required", c.SourceFile)
	}

	return Channel{
		ID:       c.ID,
		Platform: c.Platform,
		Handle:   c.Handle,
		Language: lang,
		Niche:    c.Niche,
		Brand: BrandKit{
			Primary:      c.Brand.Primary,
			Secondary:    c.Brand.Secondary,
			FontHeading:  c.Brand.FontHeading,
			FontBody:     c.Brand.FontBody,
			CaptionStyle: captionStyle,
		},
		Voice: Voice{
			Engine:  c.Voice.Engine,
			VoiceID: c.Voice.VoiceID,
			Speed:   c.Voice.Speed,
		},
		Formats: append([]string(nil), c.Formats...),
		Windows: Windows{
			TZ:    c.Windows.TZ,
			Slots: append([]string(nil), c.Windows.Slots...),
		},
		Disclaimer: c.Disclaimer,
		SourceFile: c.SourceFile,
	}, nil
}

// Record is a row of the `channels` table (ARCHITECTURE §4).
type Record struct {
	ID              string
	Platform        string
	Handle          string
	Language        string
	Niche           string
	AccountRef      string
	Status          string
	WarmupStartedAt *time.Time
}

// DB is the subset of *sql.DB that this package needs, so callers can pass a
// *sql.Tx too.
type DB interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// SyncChannels upserts each channel into the `channels` table and returns
// the resulting rows in the same order as channels. On first insert, status
// is set to "warming" and warmup_started_at to now (UTC). On an existing
// channel, only platform/handle/language/niche are refreshed from config;
// status, account_ref and warmup_started_at are left as-is since they are
// runtime-owned, not config-owned.
func SyncChannels(ctx context.Context, db DB, channels []Channel, now time.Time) ([]Record, error) {
	out := make([]Record, 0, len(channels))
	for _, ch := range channels {
		rec, err := upsertChannel(ctx, db, ch, now)
		if err != nil {
			return out, fmt.Errorf("sync channel %s: %w", ch.ID, err)
		}
		out = append(out, rec)
	}
	return out, nil
}

func upsertChannel(ctx context.Context, db DB, ch Channel, now time.Time) (Record, error) {
	row := db.QueryRowContext(ctx, `
INSERT INTO channels (id, platform, handle, language, niche, account_ref, status, warmup_started_at)
VALUES (?, ?, ?, ?, ?, '', 'warming', ?)
ON CONFLICT(id) DO UPDATE SET
    platform = excluded.platform,
    handle   = excluded.handle,
    language = excluded.language,
    niche    = excluded.niche
RETURNING id, platform, handle, language, niche, account_ref, status, warmup_started_at
`, ch.ID, ch.Platform, ch.Handle, string(ch.Language), ch.Niche, now.UTC().Format(time.RFC3339Nano))

	var rec Record
	var warmup sql.NullString
	if err := row.Scan(&rec.ID, &rec.Platform, &rec.Handle, &rec.Language, &rec.Niche, &rec.AccountRef, &rec.Status, &warmup); err != nil {
		return Record{}, fmt.Errorf("upsert: %w", err)
	}
	if warmup.Valid && warmup.String != "" {
		t, err := time.Parse(time.RFC3339Nano, warmup.String)
		if err != nil {
			return Record{}, fmt.Errorf("parse warmup_started_at %q: %w", warmup.String, err)
		}
		t = t.UTC()
		rec.WarmupStartedAt = &t
	}
	return rec, nil
}

// GetChannel fetches one channels row by id. It returns (nil, nil) if the
// channel does not exist.
func GetChannel(ctx context.Context, db DB, id string) (*Record, error) {
	row := db.QueryRowContext(ctx,
		`SELECT id, platform, handle, language, niche, account_ref, status, warmup_started_at FROM channels WHERE id = ?`,
		id,
	)
	var rec Record
	var warmup sql.NullString
	if err := row.Scan(&rec.ID, &rec.Platform, &rec.Handle, &rec.Language, &rec.Niche, &rec.AccountRef, &rec.Status, &warmup); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get channel %s: %w", id, err)
	}
	if warmup.Valid && warmup.String != "" {
		t, err := time.Parse(time.RFC3339Nano, warmup.String)
		if err != nil {
			return nil, fmt.Errorf("parse warmup_started_at %q: %w", warmup.String, err)
		}
		t = t.UTC()
		rec.WarmupStartedAt = &t
	}
	return &rec, nil
}

// Caps is the cadence cap for a channel on a given day (COMPLIANCE §4).
type Caps struct {
	ShortsPerDay int
	LongPerWeek  int
}

// CapsFor implements the COMPLIANCE §4 ramp-up table: caps grow with the
// number of weeks since the channel's warmup_started_at. A channel with no
// warmup_started_at yet (not synced/activated) gets zero caps. Week 4+ caps
// use the upper bound of COMPLIANCE's 3-4 long/week range; picking a target
// within that range is a scheduler concern, not a cap concern.
func CapsFor(rec Record, date time.Time) Caps {
	if rec.WarmupStartedAt == nil {
		return Caps{ShortsPerDay: 0, LongPerWeek: 0}
	}

	start := rec.WarmupStartedAt.UTC().Truncate(24 * time.Hour)
	day := date.UTC().Truncate(24 * time.Hour)

	daysSince := int(day.Sub(start).Hours() / 24)
	week := daysSince/7 + 1
	if week < 1 {
		week = 1
	}

	switch {
	case week == 1:
		return Caps{ShortsPerDay: 1, LongPerWeek: 1}
	case week == 2:
		return Caps{ShortsPerDay: 1, LongPerWeek: 2}
	case week == 3:
		return Caps{ShortsPerDay: 2, LongPerWeek: 2}
	default: // week 4+
		return Caps{ShortsPerDay: 2, LongPerWeek: 4}
	}
}
