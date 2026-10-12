package main

import (
	"context"
	"github.com/euler-gaozl/agent-os-sandbox/sandbox"
	"github.com/euler-gaozl/agent-os-sdk/servicehost"
	"github.com/euler-gaozl/agent-os-sdk/servicehttp"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/http"
)

func main() {
	servicehost.Main(servicehost.Options{Role: "sandbox", Port: "8093", Database: true, Concurrency: 16, Authorize: sandbox.Authorize, Readiness: sandbox.Readiness}, func(ctx context.Context, p *pgxpool.Pool, c servicehttp.Config) (http.Handler, func(context.Context) error, error) {
		if err := applyLocalDefaults(); err != nil {
			return nil, nil, err
		}
		client := servicehost.Client()
		store := &sandbox.Store{DB: p, Remote: &client}
		s := &sandbox.Server{Store: store, Token: c.Token, RuntimeURL: client.RuntimeURL}
		return s.SandboxHandler(c), s.ReapSandbox, nil
	})
}
