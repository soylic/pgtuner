package pg

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Options carries connection parameters for Connect.
type Options struct {
	DSN string // libpq-style. Empty or partial values fall back to PG* env and ~/.pgpass
	// StatementTimeout limits each query. Zero means DefaultStatementTimeout. A negative value turns it off.
	StatementTimeout time.Duration
}

// DefaultStatementTimeout bounds each query so a heavy catalog scan cannot hold a production server.
const DefaultStatementTimeout = 30 * time.Second

// lockTimeout is how long a query may wait for a lock before it fails.
const lockTimeout = 5 * time.Second

// Conn wraps a pgx connection and exposes the detected server version.
type Conn struct {
	pgx     *pgx.Conn
	Version ServerVersion
}

// Connect opens a connection and detects the server version. The caller must call Close.
func Connect(ctx context.Context, o Options) (*Conn, error) {
	cfg, err := pgx.ParseConfig(o.DSN)
	if err != nil {
		return nil, fmt.Errorf("parse DSN: %w", err)
	}
	return connectConfig(ctx, cfg, o)
}

// ConnectDB connects with the DSN parameters but with the database set to dbname.
// -all-databases uses it to inspect each database. The caller must call Close.
func ConnectDB(ctx context.Context, o Options, dbname string) (*Conn, error) {
	cfg, err := pgx.ParseConfig(o.DSN)
	if err != nil {
		return nil, fmt.Errorf("parse DSN: %w", err)
	}
	cfg.Database = dbname
	return connectConfig(ctx, cfg, o)
}

// connectConfig opens the connection, names the session, and makes it read-only
// with query and lock time limits. Each limit is a plain SET after connect,
// because a connection pooler can reject startup parameters. If a SET fails
// (some hosted services forbid it), the session runs without that limit.
func connectConfig(ctx context.Context, cfg *pgx.ConnConfig, o Options) (*Conn, error) {
	if cfg.RuntimeParams == nil {
		cfg.RuntimeParams = map[string]string{}
	}
	if cfg.RuntimeParams["application_name"] == "" {
		cfg.RuntimeParams["application_name"] = "pgtuner"
	}
	pc, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	guardSession(ctx, pc, o)
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

// ListUserDatabases returns connectable, non-template database names, ordered by name.
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

func guardSession(ctx context.Context, pc *pgx.Conn, o Options) {
	_, _ = pc.Exec(ctx, "SET default_transaction_read_only = on")
	_, _ = pc.Exec(ctx, fmt.Sprintf("SET lock_timeout = %d", lockTimeout.Milliseconds()))
	timeout := o.StatementTimeout
	if timeout == 0 {
		timeout = DefaultStatementTimeout
	}
	if timeout > 0 {
		_, _ = pc.Exec(ctx, fmt.Sprintf("SET statement_timeout = %d", timeout.Milliseconds()))
	}
}

func (c *Conn) Close(ctx context.Context) { _ = c.pgx.Close(ctx) }

func (c *Conn) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return c.pgx.Query(ctx, sql, args...)
}

func (c *Conn) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return c.pgx.QueryRow(ctx, sql, args...)
}
