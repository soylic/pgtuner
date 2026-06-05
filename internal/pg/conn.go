package pg

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Options carries connection parameters for Connect.
type Options struct {
	DSN string // libpq-style; partial/empty falls back to PG* env + ~/.pgpass via pgx
}

// Conn wraps a pgx connection and exposes the detected server version.
type Conn struct {
	pgx     *pgx.Conn
	Version ServerVersion
}

// Connect opens a new PostgreSQL connection, detects the server version, and
// returns a ready-to-use Conn. The caller must call Close when done.
func Connect(ctx context.Context, o Options) (*Conn, error) {
	cfg, err := pgx.ParseConfig(o.DSN)
	if err != nil {
		return nil, fmt.Errorf("parse DSN: %w", err)
	}
	return connectConfig(ctx, cfg)
}

// ConnectDB opens a connection using the same parameters as the given DSN but
// with the database name overridden to dbname. It is used for per-database
// analysis (-all-databases), where each user database is inspected over its own
// connection. The caller must call Close when done.
func ConnectDB(ctx context.Context, dsn, dbname string) (*Conn, error) {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse DSN: %w", err)
	}
	cfg.Database = dbname
	return connectConfig(ctx, cfg)
}

// connectConfig connects with the given config and detects the server version.
func connectConfig(ctx context.Context, cfg *pgx.ConnConfig) (*Conn, error) {
	pc, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	var num string
	if err := pc.QueryRow(ctx, "SHOW server_version_num").Scan(&num); err != nil {
		_ = pc.Close(ctx)
		return nil, err
	}
	v, err := ParseServerVersionNum(num)
	if err != nil {
		_ = pc.Close(ctx)
		return nil, err
	}
	return &Conn{pgx: pc, Version: v}, nil
}

// ListUserDatabases returns the names of connectable, non-template databases in
// the cluster, ordered by name. Used by -all-databases.
// src: https://www.postgresql.org/docs/current/catalog-pg-database.html
func ListUserDatabases(ctx context.Context, c *Conn) ([]string, error) {
	const sql = "SELECT datname FROM pg_database WHERE NOT datistemplate AND datallowconn ORDER BY datname"
	rows, err := c.Query(ctx, sql)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

// Close closes the underlying pgx connection.
func (c *Conn) Close(ctx context.Context) { _ = c.pgx.Close(ctx) }

// Query runs a query on the underlying connection. Used by the pg-stats helpers and checks.
func (c *Conn) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return c.pgx.Query(ctx, sql, args...)
}

// QueryRow runs a single-row query on the underlying connection.
func (c *Conn) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return c.pgx.QueryRow(ctx, sql, args...)
}
