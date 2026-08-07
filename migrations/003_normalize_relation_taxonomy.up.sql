-- `influences` was a pre-architecture relation name. The Core taxonomy owns
-- `causes`; quarantine stale suggestions and retire any effective legacy
-- relation while preserving every row for audit/history.
--
-- The counters are emitted by the migration itself so an operator can retain
-- an auditable reconciliation record without deleting or rewriting history.
DO $$
DECLARE
    suggestion_total   BIGINT;
    suggestion_pending BIGINT;
    suggestion_other   BIGINT;
    relation_total     BIGINT;
    relation_active    BIGINT;
    suggestions_changed BIGINT;
    relations_changed   BIGINT;
BEGIN
    SELECT COUNT(*) INTO suggestion_total
    FROM relation_suggestions
    WHERE relation_type = 'influences';

    SELECT COUNT(*) INTO suggestion_pending
    FROM relation_suggestions
    WHERE relation_type = 'influences' AND status = 'pending';

    SELECT COUNT(*) INTO suggestion_other
    FROM relation_suggestions
    WHERE relation_type = 'influences' AND status <> 'pending' AND status <> 'rejected';

    SELECT COUNT(*) INTO relation_total
    FROM relations
    WHERE relation_type = 'influences';

    SELECT COUNT(*) INTO relation_active
    FROM relations
    WHERE relation_type = 'influences' AND effective_to IS NULL;

    RAISE NOTICE 'relation taxonomy reconciliation before: suggestions_total=%, pending=%, other_non_rejected=%, relations_total=%, active_to_retire=%',
        suggestion_total, suggestion_pending, suggestion_other, relation_total, relation_active;

    UPDATE relation_suggestions
    SET status = 'rejected',
        review_reason = concat_ws('; ',
            NULLIF(review_reason, ''),
            format('superseded by canonical relation type: causes (prior_status=%s)', status)),
        updated_at = NOW()
    WHERE relation_type = 'influences'
      AND status <> 'rejected';
    GET DIAGNOSTICS suggestions_changed = ROW_COUNT;

    UPDATE relations
    SET effective_to = NOW(),
        updated_at = NOW()
    WHERE relation_type = 'influences'
      AND effective_to IS NULL;
    GET DIAGNOSTICS relations_changed = ROW_COUNT;

    RAISE NOTICE 'relation taxonomy reconciliation complete: suggestions_changed=%, relations_changed=%',
        suggestions_changed, relations_changed;
END $$;
