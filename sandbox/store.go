package sandbox

import (
	"github.com/euler-gaozl/agent-os-sdk/serviceclient"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	DB     *pgxpool.Pool
	Remote *serviceclient.Client
}
