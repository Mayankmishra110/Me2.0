-- M2-124: tag script_fingerprints embeddings with the model that produced
-- them, so G2 (compliance originality gate) never compares vectors from two
-- different embedding models (e.g. Ollama nomic-embed-text vs Gemini
-- gemini-embedding-001 — different dimensionality/semantics). Existing rows
-- (all produced by nomic-embed-text, the only embed model before this
-- ticket) default to that model id; new rows always write their real model.
-- Append-only; never edit 001_init.sql or 002_agency_leads.sql.
ALTER TABLE script_fingerprints ADD COLUMN embed_model TEXT NOT NULL DEFAULT 'nomic-embed-text';
