WITH actual AS (
    SELECT c.relname AS name,
           c.relkind = 'r' AND c.relpersistence = 'p' AND NOT c.relispartition AS ordinary,
           (SELECT jsonb_agg(jsonb_build_array(a.attname, t.typname, a.atttypmod, a.attnotnull, a.attidentity)
                             ORDER BY a.attnum)
            FROM pg_attribute a
            JOIN pg_type t ON t.oid = a.atttypid
            WHERE a.attrelid = c.oid AND a.attnum > 0 AND NOT a.attisdropped) AS columns,
           NOT EXISTS (
               SELECT 1 FROM pg_attribute a
               JOIN pg_type t ON t.oid = a.atttypid
               WHERE a.attrelid = c.oid AND a.attnum > 0 AND NOT a.attisdropped
                 AND (t.typnamespace <> 'pg_catalog'::regnamespace OR a.attcollation <> t.typcollation
                      OR a.atthasdef OR a.attgenerated <> '')
           ) AS standard_columns,
           NOT EXISTS (
               SELECT 1 FROM pg_index i
               WHERE i.indrelid = c.oid AND i.indisunique
                 AND NOT EXISTS (SELECT 1 FROM pg_constraint k WHERE k.conrelid = c.oid AND k.conindid = i.indexrelid)
           ) AS declared_uniqueness,
           (SELECT jsonb_agg(key ORDER BY key)
            FROM (
                SELECT jsonb_build_array(k.contype, k.condeferrable, k.condeferred, k.convalidated,
                           ARRAY(SELECT a.attname FROM unnest(k.conkey) WITH ORDINALITY AS col(num, pos)
                                 JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum = col.num
                                 ORDER BY col.pos)) AS key
                FROM pg_constraint k
                WHERE k.conrelid = c.oid AND k.contype IN ('p','u','f','c','x')
            ) keys) AS constraints
    FROM pg_class c
    JOIN pg_namespace n ON n.oid = c.relnamespace
    WHERE n.nspname = $1 AND c.relkind IN ('r','p','f')
)
SELECT COALESCE(actual.name, expected.name) AS name
FROM actual
FULL JOIN jsonb_each($2::jsonb) AS expected(name, layout) ON actual.name = expected.name
WHERE expected.name IS NULL OR actual.ordinary IS NOT TRUE
   OR actual.standard_columns IS NOT TRUE OR actual.declared_uniqueness IS NOT TRUE
   OR actual.columns IS DISTINCT FROM expected.layout->'columns'
   OR actual.constraints IS DISTINCT FROM expected.layout->'constraints'
ORDER BY 1
LIMIT 1
