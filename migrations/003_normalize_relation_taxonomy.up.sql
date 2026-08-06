-- `influences` was a pre-architecture relation name. The Core taxonomy owns
-- `causes`; quarantine stale suggestions and retire any effective legacy
-- relation while preserving every row for audit/history.
UPDATE relation_suggestions
SET status = 'rejected',
    review_reason = 'superseded by canonical relation type: causes',
    updated_at = NOW()
WHERE relation_type = 'influences'
  AND status = 'pending';

UPDATE relations
SET effective_to = NOW(),
    updated_at = NOW()
WHERE relation_type = 'influences'
  AND effective_to IS NULL;
