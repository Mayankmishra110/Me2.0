-- M2-603: agency leads table (ARCHITECTURE §4 addition). Append-only; never edit 001_init.sql.
CREATE TABLE agency_leads (
    id         TEXT PRIMARY KEY,
    company    TEXT NOT NULL,
    contact    TEXT NOT NULL DEFAULT '',
    niche_fit  TEXT NOT NULL DEFAULT '',
    status     TEXT NOT NULL DEFAULT 'new'
        CHECK (status IN ('new', 'contacted', 'proposal_sent', 'won', 'lost')),
    source     TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
);

CREATE TABLE agency_proposals (
    id         TEXT PRIMARY KEY,
    lead_id    TEXT NOT NULL REFERENCES agency_leads (id),
    draft_text TEXT NOT NULL DEFAULT '',
    status     TEXT NOT NULL DEFAULT 'drafting'
        CHECK (status IN ('drafting', 'pending_approval', 'approved', 'rejected')),
    approval_id TEXT,
    created_at TEXT NOT NULL
);

CREATE INDEX agency_leads_status ON agency_leads (status);
