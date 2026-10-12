package sandbox

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5/pgxpool"
)

const readinessQuery = "SELECT input_hash,container_name,status,result,cleaned_at FROM sandbox_jobs WHERE false"

// Readiness checks only this service's schema and permissions without reading user data.
func Readiness(ctx context.Context, db *pgxpool.Pool) error {
	if db == nil {
		return errors.New("database required")
	}
	_, err := db.Exec(ctx, readinessQuery)
	return err
}
