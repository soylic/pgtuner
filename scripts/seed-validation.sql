-- seed-validation.sql: create dead tuples, a temp-file spill, and schema anomalies for pgtuner tests.
--
-- Dead tuples: autovacuum is off on the table, so the dead tuples stay during the test.
-- n_dead_tup in pg_stat_user_tables shows them at once after the DELETE.
--
-- Temp-file spill: work_mem of 64 kB forces a large sort to spill to a temp file.
-- The spill shows in pg_stat_database.temp_files and temp_bytes.
--
-- Schema anomalies (Epic 3):
--   - unused index (idx_scan=0, size > 1 MiB)
--   - invalid index (indisvalid=false, simulated via catalog update as superuser)
--   - table without primary key (size > 1 MiB)
--   - FK without supporting index (child references parent, no index on FK col)
--   - table bloat (dead_ratio > 20 %, reuses pgtuner_bloat above)

-- Bloat table, used by both the autovacuum and the schema bloat checks.
DROP TABLE IF EXISTS pgtuner_bloat;
CREATE TABLE pgtuner_bloat (id int PRIMARY KEY, payload text);
ALTER TABLE pgtuner_bloat SET (autovacuum_enabled = false);

INSERT INTO pgtuner_bloat
  SELECT g, repeat('x', 100)
  FROM generate_series(1, 50000) g;

-- About 25 000 dead tuples, so dead_ratio is about 50 %.
DELETE FROM pgtuner_bloat WHERE id % 2 = 0;

-- work_mem of 64 kB is far below what an in-memory sort of 200 000 integers needs.
SET work_mem = '64kB';
SET log_temp_files = 0;
SELECT count(*) FROM (
  SELECT * FROM generate_series(1, 200000) ORDER BY random()
) s;

-- Unused index (Epic 3 sub-check 1). 150 000 rows of 200 bytes put the index well over 1 MiB.
-- Nothing queries it, so idx_scan stays 0.
-- src: monitoring-stats pg_stat_all_indexes
DROP TABLE IF EXISTS pgtuner_unused_idx;
CREATE TABLE pgtuner_unused_idx (
    id      bigserial PRIMARY KEY,
    payload text
);
ALTER TABLE pgtuner_unused_idx SET (autovacuum_enabled = false);
INSERT INTO pgtuner_unused_idx (payload)
    SELECT repeat('y', 200) FROM generate_series(1, 150000);
CREATE INDEX pgtuner_unused_idx_payload ON pgtuner_unused_idx(payload);

-- Invalid index (Epic 3 sub-check 2). The seed runs as a throwaway superuser, so it can
-- set indisvalid to false to simulate a failed CREATE INDEX CONCURRENTLY.
-- src: sql-createindex.html
DROP TABLE IF EXISTS pgtuner_invalid_idx_tbl;
CREATE TABLE pgtuner_invalid_idx_tbl (id int, val text);
INSERT INTO pgtuner_invalid_idx_tbl SELECT g, 'x' FROM generate_series(1, 1000) g;
CREATE INDEX pgtuner_invalid_idx ON pgtuner_invalid_idx_tbl(id);
UPDATE pg_index
   SET indisvalid = false
 WHERE indexrelid = 'pgtuner_invalid_idx'::regclass;

-- Table without primary key (Epic 3 sub-check 4). 50 000 rows of 200 bytes exceed 1 MiB.
-- src: ddl-constraints.html
DROP TABLE IF EXISTS pgtuner_no_pk;
CREATE TABLE pgtuner_no_pk (
    col1 text,
    col2 text
);
ALTER TABLE pgtuner_no_pk SET (autovacuum_enabled = false);
INSERT INTO pgtuner_no_pk
    SELECT repeat('a', 100), repeat('b', 100)
    FROM generate_series(1, 50000);

-- Foreign key without supporting index (Epic 3 sub-check 5).
-- src: ddl-constraints.html
DROP TABLE IF EXISTS pgtuner_fk_child CASCADE;
DROP TABLE IF EXISTS pgtuner_fk_parent CASCADE;
CREATE TABLE pgtuner_fk_parent (id int PRIMARY KEY, name text);
INSERT INTO pgtuner_fk_parent SELECT g, 'row' || g FROM generate_series(1, 100) g;
CREATE TABLE pgtuner_fk_child (
    id        bigserial PRIMARY KEY,
    parent_id int REFERENCES pgtuner_fk_parent(id),  -- no index on parent_id
    payload   text
);
INSERT INTO pgtuner_fk_child (parent_id, payload)
    SELECT (g % 100) + 1, repeat('z', 50)
    FROM generate_series(1, 10000) g;
