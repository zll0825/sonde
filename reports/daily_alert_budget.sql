-- Read-only UTC daily alert budget report.
-- Parameters: $1 start date (inclusive), $2 end date (inclusive), $3 real limit.
WITH params AS (
    SELECT $1::date AS start_day, $2::date AS end_day, $3::int AS real_limit
),
days AS (
    SELECT generate_series(p.start_day, p.end_day, interval '1 day')::date AS utc_day
    FROM params p
),
counts AS (
    SELECT
        (a.triggered_at AT TIME ZONE 'UTC')::date AS utc_day,
        count(*) FILTER (WHERE a.source_class = 'real') AS real_count,
        count(*) FILTER (WHERE a.source_class = 'mock') AS mock_count,
        count(*) FILTER (WHERE a.source_class = 'test') AS test_count,
        count(*) FILTER (WHERE a.source_class = 'unknown') AS unknown_count
    FROM alerts a
    CROSS JOIN params p
    WHERE a.triggered_at >= p.start_day AT TIME ZONE 'UTC'
      AND a.triggered_at < (p.end_day + 1) AT TIME ZONE 'UTC'
      AND a.mode = 'live'
    GROUP BY 1
)
SELECT
    d.utc_day,
    COALESCE(c.real_count, 0) AS real_count,
    COALESCE(c.mock_count, 0) AS mock_count,
    COALESCE(c.test_count, 0) AS test_count,
    COALESCE(c.unknown_count, 0) AS unknown_count,
    p.real_limit,
    COALESCE(c.real_count, 0) <= p.real_limit AS within_budget
FROM days d
CROSS JOIN params p
LEFT JOIN counts c USING (utc_day)
ORDER BY d.utc_day;
