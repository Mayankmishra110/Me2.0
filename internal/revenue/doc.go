// Package revenue is the single place every dollar/rupee entering the
// pipeline gets recorded (ARCHITECTURE.md §4 `revenue` table; SPEC.md §4
// GET/POST /api/revenue; SPEC.md §1 P6 exit check "Revenue screen shows
// real entries").
//
// Entries come from two paths:
//   - Manual: a human (via the dashboard's RevenuePage or, later, Telegram)
//     records an affiliate/sponsor/agency/saas amount that already happened.
//   - Semi-automated pull: PullChannelAdsRevenue reads ad-revenue figures
//     for a channel/platform/period from a RevenueAPI (YouTube Analytics'
//     monetary reports today) and upserts one "ads" line entry per
//     channel+platform+period, idempotently (re-running a pull for a period
//     that already has an entry updates it in place, never duplicates it).
//
// Revenue entries are internal records, not public/outbound content, so
// D5's approval.request gate does not apply here (CLAUDE.md's "nothing
// public without approval" is about publish paths; this package only
// records money that already happened).
package revenue
