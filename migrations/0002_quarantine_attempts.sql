-- Phase 2: quarantine replay bookkeeping + category lookup index
ALTER TABLE quarantine_events ADD COLUMN IF NOT EXISTS attempts INTEGER NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS idx_quarantine_events_raw_category ON quarantine_events ((raw_payload->>'category'));
