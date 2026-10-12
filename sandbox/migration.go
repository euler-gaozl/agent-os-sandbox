package sandbox

import (
	"context"
	_ "embed"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/sandbox.sql
var migrationSQL string

// Migrate owns only sandbox's tables. Provisioning roles and database credentials
// belongs to deployment. The application role must already exist; failure rolls
// back both DDL and grants. No data/config import or destructive down migration.
func Migrate(ctx context.Context, db *pgxpool.Pool, applicationRole string) error {
	if applicationRole == "" {
		return errors.New("application role required")
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SET LOCAL search_path=public; SET LOCAL lock_timeout='3s'; SET LOCAL statement_timeout='120s'"); err != nil {
		return err
	}
	// Retain the legacy migration lock while mixed versions are supported.
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(8843921)"); err != nil {
		return err
	}
	var exists bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)", applicationRole).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return errors.New("application role is not provisioned")
	}
	if _, err = tx.Exec(ctx, migrationSQL); err != nil {
		return err
	}
	for _, table := range []string{"sandbox_jobs"} {
		if _, err = tx.Exec(ctx, "GRANT SELECT,INSERT,UPDATE,DELETE ON TABLE "+pgx.Identifier{"public", table}.Sanitize()+" TO "+pgx.Identifier{applicationRole}.Sanitize()); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
