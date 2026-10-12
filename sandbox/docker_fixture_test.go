package sandbox

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/euler-gaozl/agent-os-sdk/serviceclient"
	"github.com/euler-gaozl/agent-os-sdk/servicehttp"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// This owner fixture has only Sandbox tables and an actual restricted login.
// Runtime authorization is an explicit HTTP fixture, not a copied execution
// domain or proof of live Runtime lease/intent integration.
func sandboxDockerFixture(t *testing.T, invocations ...Invocation) (*Store, string, string) {
	t.Helper()
	db, role := fixtureDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := Migrate(ctx, db, role); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "ALTER ROLE "+pgx.Identifier{role}.Sanitize()+" LOGIN PASSWORD 'fixture-only'"); err != nil {
		t.Fatal(err)
	}
	// A fresh URL is needed: pgx ConnString retains the parsed input even after
	// changing the struct's User/Database fields.
	u, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		t.Fatal("fixture requires a PostgreSQL URL")
	}
	u.User = url.UserPassword(role, "fixture-only")
	u.Path = "/" + db.Config().ConnConfig.Database
	q := u.Query()
	for k := range q {
		switch strings.ToLower(k) {
		case "user", "password", "dbname", "database", "search_path", "pool_max_conns":
			q.Del(k)
		}
	}
	q.Set("search_path", "public")
	q.Set("pool_max_conns", "3")
	u.RawQuery = q.Encode()
	dsn := u.String()
	app, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal("restricted fixture connection unavailable")
	}
	t.Cleanup(app.Close)
	var currentRole, currentDatabase string
	if err = app.QueryRow(ctx, "SELECT current_user,current_database()").Scan(&currentRole, &currentDatabase); err != nil || currentRole != role || currentDatabase != db.Config().ConnConfig.Database {
		t.Fatal("restricted fixture identity mismatch", err)
	}
	if err = Readiness(ctx, app); err != nil {
		t.Fatal("restricted fixture not ready", err)
	}
	token := "sandbox-fixture-runtime-authorization-only"
	authority := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/sandbox/authorize" || r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "fixture authorization denied", 403)
			return
		}
		var got Invocation
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536)).Decode(&got) != nil {
			http.Error(w, "invalid invocation", 400)
			return
		}
		allowed := false
		for _, expected := range invocations {
			allowed = allowed || sameSandboxJSON(JSON(got), JSON(expected))
		}
		servicehttp.JSONResponse(w, 200, map[string]bool{"authorized": allowed})
	}))
	t.Cleanup(authority.Close)
	return &Store{DB: app, Remote: &serviceclient.Client{RuntimeURL: authority.URL, Token: token}}, dsn, authority.URL
}

func TestDockerFixtureRestrictedIdentityAndRuntimeAuthorization(t *testing.T) {
	runID := ID()
	inv := Invocation{RunID: runID, OperationID: runID + "/tool/fixture", Owner: "fixture", Fence: 1, Name: "sandbox_exec", Arguments: JSON(map[string]string{"code": "console.log('fixture')"})}
	store, _, _ := sandboxDockerFixture(t, inv)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	server := &Server{Store: store}
	if err := server.validateSandboxInvocation(ctx, inv); err != nil {
		t.Fatal(err)
	}
	inv.Fence++
	if err := server.validateSandboxInvocation(ctx, inv); err == nil {
		t.Fatal("fixture accepted a different fence")
	}
	if _, err := store.DB.Exec(ctx, "CREATE TABLE forbidden_fixture_ddl(id int)"); err == nil {
		t.Fatal("fixture application login can create tables")
	}
}
