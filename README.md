# Agent OS Sandbox

Independent HTTP broker for isolated execution. Owns only `public.sandbox_jobs`.
It authorizes each invocation through Runtime's HTTP contract, retaining the
operation identity and fence. It cannot query execution tables. Missing isolation
configuration rejects execution; there is no host-shell fallback.

## Build and dependencies

Go 1.27.2, PostgreSQL 18; run `scripts/Build.ps1` with PowerShell 7 or
`GOWORK=off go build -mod=vendor ./cmd/sandbox`. Committed vendor and the pinned
SDK in `source-provenance.json` allow a single checkout without private tokens.
No Node is needed by the broker. Start the binary with `--config <service.json>`.
`deploy/service.example.json` contains placeholders only. Supply unique service
identity, peer hashes (including self), scoped DB URL and `RUNTIME_URL`.

Execution additionally requires a dedicated/restricted Linux Docker daemon and
a workload image selected by immutable `SANDBOX_IMAGE` digest. Build that image
with `deploy/sandbox-runtime.Dockerfile`; it contains the guard and Node 24.
The broker container includes Docker CLI and runs as non-root; explicitly grant
only the required daemon socket group access or configure the dedicated daemon.
Docker daemon access is privileged infrastructure access; do not share a general
host daemon with untrusted workloads or expose the broker publicly.

Only Runtime's identity can POST `/execute`. Containers use read-only root,
disabled network, bounded memory/CPU/processes/output and a finite deadline;
the guard drops workload privileges. Reconciliation uses durable job/container
identity, not the assumption that a missing response means no work happened.
Windows currently supports the broker and fail-closed path; local isolated
execution remains unavailable until a Linux daemon and image are configured.

## Migration / release

Provision a non-owner application role with public schema USAGE and no CREATE.
Supply DDL connection only through `MIGRATION_DATABASE_URL`, then run
`bin/migrate --role <existing-role>`. The transaction owns only sandbox_jobs,
applies bounded locking and grants its four DML privileges. Normal startup has
no DDL authority and verifies the schema before background cleanup.

Release or roll back this broker independently while preserving its DB/config
and compatible Runtime protocol. Never remove job records to force retries.
`deploy/release-contract.json` identifies the existing data/HTTP contract.
Integration Windows catalog activation is being connected as part of P2.

Run `go vet -mod=vendor ./...` and `go test -mod=vendor -count=1 ./...`.
`TEST_DATABASE_URL` enables real isolated PostgreSQL migration/permission tests;
the administrator must be able to create fixture DBs/NOLOGIN roles. Tests do not
migrate the given DB. Without it they explicitly skip. Boundary tests use an
HTTP Runtime fixture, not a full Runtime. Existing whole-platform Docker crash
tests remain in the integration repository until separately ported/accepted;
broker compilation is not proof that isolated execution passed.

`sandbox/docker_integration_test.go` now retains the real Docker isolation,
replay, broker subprocess kill, independent guard deadline and orphan receipt
recovery scenarios in this owner. It uses its own disposable database and
restricted login; Runtime authorization is an explicit HTTP fixture. Run with
`TEST_DOCKER_SANDBOX=1`, a dedicated `DOCKER_HOST`, a digest-pinned
`SANDBOX_IMAGE` and fixture `TEST_DATABASE_URL`. The opt-in `isolation.yml`
workflow provides that daemon and builds this repository's guard. It has no
push/PR trigger. This new owner Docker workflow has not yet run; the historical
monorepo test remains until final unified Linux acceptance. Local fixture
identity/authorization checks do not establish Docker execution on Windows.

Local proof `p2-owner-coverage-58d357e0cd7b4e71bb4ea51cf7661743` passed
three targeted authorization/restricted-login checks with real PostgreSQL and
zero skips; the Linux test binary also cross-compiled. The transferred Docker
test now requires an actual SIGKILL wait status and stops/waits for the broker
before removing the container on failure. Static review identified those gaps
in the inherited test. Actual Docker execution remains pending.

Legacy local configurations may omit the Runtime address: this service preserves
the previous loopback default (127.0.0.1:8094) for its bounded heartbeat/client.
Where used, Registry (8096) and ToolHub (8091) defaults are also owned by the
service entrypoint. Explicit environment/config endpoints always take precedence.
Configure these addresses explicitly for containers or multiple hosts. No config
file or shared SDK role policy is rewritten by this compatibility behavior.
