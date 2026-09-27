# Compliance & Originality Engine

> Principle ([CONTEXT.md D18](CONTEXT.md#4-decision-log)): we do not hide mass production from platforms; we make content that **actually meets** their rules. Accounts stay safe because every item is original, sourced, honest and paced — and a human approves it.
>
> Platform policies change. Before building or changing a gate, re-read the linked policy pages and update this file. Anything marked *(verify)* must be checked at implementation time.

## 1. Rules we design against

| Platform | Rule | What it means for us |
|---|---|---|
| YouTube | **Inauthentic content** (formerly "repetitious"): mass-produced or template-identical videos are not monetizable | Real research per video, native scripts, varied formats, human approval, steady cadence |
| YouTube | **Reused content** | Never use other creators' footage/audio; stock only with license; our own voice-over and commentary carry the value |
| YouTube | **Spam, deceptive practices & scams**: misleading titles/thumbnails, tag stuffing, repetitive comments | Metadata honesty gate; max 15 tags; no comments automation |
| YouTube | **Altered or synthetic content disclosure** for realistic AI media | Set the disclosure flag when visuals/voice could be mistaken for real people or events *(verify API field)* |
| YouTube | **Paid promotion** checkbox; **made for kids** setting | Set explicitly on every upload |
| YouTube | **Advertiser-friendly guidelines** | Avoid shocking thumbnails, profanity in the first 30 s, sensitive topics without context |
| YouTube | Copyright / Content ID | Music only from YouTube Audio Library or licensed packs, logged per asset |
| Meta (IG/FB) | **Unoriginal content** gets reduced reach and no monetization; branded content rules; AI labels | Same originality gate; publish our own renders only; "Paid partnership"/`#ad` when affiliate |
| X | **Platform manipulation & spam**, automation rules: no identical posts across multiple accounts, no bulk/aggressive automation | Business and personal X accounts **never** share posts; per-account daily caps; no auto-likes/follows/replies |
| Pinterest | Spam and affiliate rules | Affiliate links allowed with disclosure; one pin per destination per day max; no link cloakers |
| LinkedIn | Automation / API terms | Only our own posts via the official API |
| US FTC | Endorsement guides | Clear affiliate disclosure in descriptions and captions |
| India SEBI | Finfluencer rules | Hindi finance channel is education-only: no stock tips, no buy/sell calls, no return promises, standard disclaimer |
| All | Fake engagement | Never buy views/subs/followers; no sub-for-sub; no engagement pods |

## 2. Allowed inputs

| Input | Allowed use |
|---|---|
| Topic text from Mayank or the Scout | ✅ Full use |
| Articles, papers, reports, public data | ✅ As facts with source URLs in the brief; never copied as script text |
| Another creator's video URL | ✅ **Transcript as research notes only.** ❌ No footage, audio, thumbnails, or script structure copying |
| Mayank's own recordings | ✅ Full use |
| Pexels / Pixabay | ✅ With license URL stored in `assets.license_url` |
| AI-generated visuals | ✅ With disclosure when realistic |
| Music | ✅ YouTube Audio Library / licensed packs only |

## 3. Gates

Every content item passes **script gates** (before voice/render) and **final gates** (before approval). Results are stored in `content_items.compliance` and shown on the approval screen.

### Script gates

| Gate | Check | Default threshold |
|---|---|---|
| G1 Source originality | Longest shared 8-word shingle run and overall shingle overlap with each source text | overlap < 5%, no shared run > 12 words |
| G2 Self-originality | Embedding cosine similarity vs this channel's last 200 scripts; title similarity vs last 30 titles | cosine < 0.90; title Jaccard < 0.6 |
| G3 Facts sourced | Every factual claim in the brief has ≥1 source URL; script numbers must appear in the brief | 100% |
| G4 Claims | Finance: no "guaranteed", "sure-shot", specific buy/sell calls, return promises; health/legal: no advice | 0 hits (LLM classifier + keyword list) |
| G5 Metadata honesty | LLM judge: does the title/thumbnail text promise something the script delivers? | "yes" |
| G6 Format variety | Same format not more than 2× in a row per channel; hook style not repeated 3× in a row | enforced by picker |
| G7 Language quality | Hindi scripts: native phrasing check (no literal-translation artifacts); English: reading level for target | LLM judge "pass" |

A failing script is rewritten with the gate feedback (max 2 retries), then dead-lettered with the report.

### Final gates

| Gate | Check |
|---|---|
| F1 Assets licensed | Every clip and music file has a license record |
| F2 Captions | Burned-in captions for Shorts/Reels; `.srt` uploaded for long videos |
| F3 Disclosures set | Synthetic-content flag decided, affiliate disclosure present when links exist, finance disclaimer present on finance channels |
| F4 Technical | Duration limits per platform, loudness −14 LUFS, resolution, no black frames > 1 s |
| F5 Cadence | Channel daily/weekly caps and ramp-up stage respected; X business/personal never duplicate |
| F6 Quota | Platform API quota available for the slot |
| F7 Human | Mayank approves on Telegram or the dashboard |

## 4. Cadence and ramp-up

| Week (per new channel) | Shorts/day | Long/week |
|---|---|---|
| 1 | 1 | 1 |
| 2 | 1 | 2 |
| 3 | 2 | 2 |
| 4+ | 2 | 3–4 |

Totals at full speed across 4 channels: **8 Shorts/day + ~2 long videos/day**. Other platforms (per account per day): Instagram Reels ≤ 3, Facebook ≤ 3, X business ≤ 5 posts, Pinterest ≤ 5 pins, X personal ≤ 3, LinkedIn ≤ 1.

Posting times come from each channel's audience windows ([CONTENT_STRATEGY.md](CONTENT_STRATEGY.md)) and, after week 4, from analytics. Times are chosen for the audience, not randomized to look human.

## 5. Output shape

```json
{
  "passed": false,
  "gates": [
    {"id": "G1", "passed": true,  "score": 0.02, "detail": "max shared run 7 words"},
    {"id": "G4", "passed": false, "detail": "phrase 'guaranteed 12% returns' in beat 3"}
  ],
  "disclosures": {"synthetic": false, "affiliate": true, "finance_disclaimer": true},
  "checked_at": "2026-09-27T10:00:00Z"
}
```
