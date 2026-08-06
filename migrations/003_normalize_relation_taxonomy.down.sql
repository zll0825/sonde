-- Only suggestions quarantined by the up migration can be safely restored.
-- Versioned authoritative relations stay retired: resurrecting them in place
-- would violate the history contract and can overwrite later review decisions.
UPDATE relation_suggestions
SET status = 'pending',
    review_reason = NULL,
    updated_at = NOW()
WHERE relation_type = 'influences'
  AND status = 'rejected'
  AND review_reason = 'superseded by canonical relation type: causes';
