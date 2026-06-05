-- seed-validation.sql: Create dead tuples, a temp-file spill, and schema-level
-- anomalies for pgtuner testing.
--
-- Dead tuples: create a table, insert 50 000 rows, delete half. autovacuum is
-- disabled on the table so the dead tuples stay for the duration of the test.
-- pg_stat_user_tables.n_dead_tup reflects dead tuples immediately after DELETE
-- because the stats are updated by the same backend.
--
-- Temp-file spill: force a large sort with work_mem set to 64 kB so PostgreSQL
-- must spill to a temp file. The spill registers in pg_stat_database.temp_files
-- and temp_bytes for the current database.
--
-- Schema anomalies (Epic 3):
--   - unused index (idx_scan=0, size > 1 MiB)
--   - invalid index (indisvalid=false, simulated via catalog update as superuser)
--   - table without primary key (size > 1 MiB)
--   - FK without supporting index (child references parent, no index on FK col)
--   - table bloat (dead_ratio > 20 % — reuses pgtuner_bloat above)

-- ===========================================================================
-- Step 1: Bloat table (reused for both autovacuum and schema bloat checks)
-- ===========================================================================
DROP TABLE IF EXISTS pgtuner_bloat;
CREATE TABLE pgtuner_bloat (id int PRIMARY KEY, payload text);
ALTER TABLE pgtuner_bloat SET (autovacuum_enabled = false);

-- Insert 50 000 rows.
INSERT INTO pgtuner_bloat
  SELECT g, repeat('x', 100)
  FROM generate_series(1, 50000) g;

-- Delete every other row (creates ~25 000 dead tuples → dead_ratio ≈ 50 %).
DELETE FROM pgtuner_bloat WHERE id % 2 = 0;

-- ===========================================================================
-- Step 2: Temp-file spill
-- ===========================================================================
-- work_mem = 64 kB is deliberately far below the memory needed to sort 200 000
-- integers in-memory, so PostgreSQL writes intermediate sort runs to temp files.
SET work_mem = '64kB';
SET log_temp_files = 0;
SELECT count(*) FROM (
  SELECT * FROM generate_series(1, 200000) ORDER BY random()
) s;

-- ===========================================================================
-- Step 3: Unused index (Epic 3 sub-check 1)
-- Create a table large enough that the index exceeds 1 MiB, then never query it
-- so idx_scan stays 0.  We insert 150 000 rows with a 200-byte text column so
-- the resulting index is comfortably over the 1 MiB (1 048 576 bytes) threshold.
-- src: monitoring-stats pg_stat_all_indexes
-- ===========================================================================
DROP TABLE IF EXISTS pgtuner_unused_idx;
CREATE TABLE pgtuner_unused_idx (
    id      bigserial PRIMARY KEY,
    payload text
);
ALTER TABLE pgtuner_unused_idx SET (autovacuum_enabled = false);
INSERT INTO pgtuner_unused_idx (payload)
    SELECT repeat('y', 200) FROM generate_series(1, 150000);
-- Create a non-unique index that will never be scanned (idx_scan=0).
CREATE INDEX pgtuner_unused_idx_payload ON pgtuner_unused_idx(payload);

-- ===========================================================================
-- Step 4: Invalid index (Epic 3 sub-check 2)
-- The seed runs as the throwaway superuser so we can directly flip indisvalid.
-- CREATE INDEX first, then immediately mark it invalid — simulates a failed
-- CREATE INDEX CONCURRENTLY build.
-- src: sql-createindex.html
-- ===========================================================================
DROP TABLE IF EXISTS pgtuner_invalid_idx_tbl;
CREATE TABLE pgtuner_invalid_idx_tbl (id int, val text);
INSERT INTO pgtuner_invalid_idx_tbl SELECT g, 'x' FROM generate_series(1, 1000) g;
CREATE INDEX pgtuner_invalid_idx ON pgtuner_invalid_idx_tbl(id);
-- Flip indisvalid=false to simulate a failed CONCURRENTLY build.
UPDATE pg_index
   SET indisvalid = false
 WHERE indexrelid = 'pgtuner_invalid_idx'::regclass;

-- ===========================================================================
-- Step 5: Table without primary key (Epic 3 sub-check 4)
-- Insert enough rows (100 000 × 200-byte payload) to exceed the 1 MiB threshold.
-- src: ddl-constraints.html
-- ===========================================================================
DROP TABLE IF EXISTS pgtuner_no_pk;
CREATE TABLE pgtuner_no_pk (
    col1 text,
    col2 text
);
ALTER TABLE pgtuner_no_pk SET (autovacuum_enabled = false);
INSERT INTO pgtuner_no_pk
    SELECT repeat('a', 100), repeat('b', 100)
    FROM generate_series(1, 50000);

-- ===========================================================================
-- Step 6: Foreign key without supporting index (Epic 3 sub-check 5)
-- Create a parent table and a child table with a FK but no index on the FK col.
-- src: ddl-constraints.html
-- ===========================================================================
DROP TABLE IF EXISTS pgtuner_fk_child CASCADE;
DROP TABLE IF EXISTS pgtuner_fk_parent CASCADE;
CREATE TABLE pgtuner_fk_parent (id int PRIMARY KEY, name text);
INSERT INTO pgtuner_fk_parent SELECT g, 'row' || g FROM generate_series(1, 100) g;
CREATE TABLE pgtuner_fk_child (
    id        bigserial PRIMARY KEY,
    parent_id int REFERENCES pgtuner_fk_parent(id),  -- FK, no index on parent_id
    payload   text
);
INSERT INTO pgtuner_fk_child (parent_id, payload)
    SELECT (g % 100) + 1, repeat('z', 50)
    FROM generate_series(1, 10000) g;
