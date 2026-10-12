package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var sandboxImageDigest = regexp.MustCompile(`^(?:[a-zA-Z0-9._:/-]+@)?sha256:[a-f0-9]{64}$`)

type boundedBuffer struct {
	bytes.Buffer
	Limit int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	left := b.Limit - b.Len()
	if left > 0 {
		if len(p) > left {
			p = p[:left]
		}
		_, _ = b.Buffer.Write(p)
	}
	return n, nil
}

func docker(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	buf := &boundedBuffer{Limit: 65536}
	cmd.Stdout = buf
	cmd.Stderr = buf
	e := cmd.Run()
	return buf.String(), e
}

func (s *Server) sandboxRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /execute", wrap(func(w http.ResponseWriter, r *http.Request) error {
		var inv Invocation
		if e := decode(w, r, &inv); e != nil {
			return e
		}
		result, e := s.sandbox(r.Context(), inv)
		if e != nil {
			return e
		}
		w.Header().Set("Content-Type", "application/json")
		_, e = w.Write(result)
		return e
	}))
}

func (s *Server) sandbox(ctx context.Context, inv Invocation) ([]byte, error) {
	image := os.Getenv("SANDBOX_IMAGE")
	if !sandboxImageDigest.MatchString(image) {
		return nil, errors.New("sandbox unavailable: configure a pinned SANDBOX_IMAGE and Docker; host execution is disabled")
	}
	if e := s.validateSandboxInvocation(ctx, inv); e != nil {
		return nil, e
	}
	if inv.Name != "sandbox_exec" {
		return nil, errors.New("sandbox only accepts sandbox_exec")
	}
	definition, e := builtinTool("sandbox_exec")
	if e != nil {
		return nil, e
	}
	if e := validateArgs(definition, inv.Arguments); e != nil {
		return nil, e
	}

	conn, e := s.Store.DB.Acquire(ctx)
	if e != nil {
		return nil, e
	}
	defer conn.Release()
	lockKey := inv.OperationID
	if _, e = conn.Exec(ctx, "SELECT pg_advisory_lock(hashtextextended($1,21))", lockKey); e != nil {
		return nil, e
	}
	defer unlockSandbox(conn, lockKey)

	if e = s.validateSandboxInvocation(ctx, inv); e != nil {
		return nil, e
	}
	var oldHash, status string
	var result []byte
	var created time.Time
	name := "agentos-" + Hash([]byte(inv.OperationID))[:32]
	hash := invocationHash(inv)
	e = conn.QueryRow(ctx, "SELECT input_hash,status,result,created_at FROM sandbox_jobs WHERE id=$1", inv.OperationID).Scan(&oldHash, &status, &result, &created)
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return nil, e
	}
	if e == nil {
		if oldHash != hash {
			return nil, errors.New("sandbox operation arguments changed")
		}
		if status == "completed" {
			cleanupSandbox(conn, inv.OperationID, name)
			return result, nil
		}
	} else {
		if e = conn.QueryRow(ctx, "INSERT INTO sandbox_jobs(id,input_hash,container_name,status) VALUES($1,$2,$3,'pending') RETURNING created_at", inv.OperationID, hash, name).Scan(&created); e != nil {
			return nil, e
		}
	}
	var args struct {
		Code string `json:"code"`
	}
	if e = json.Unmarshal(inv.Arguments, &args); e != nil {
		return nil, e
	}
	deadline := created.Add(35 * time.Second)
	if !deadline.After(time.Now()) {
		return expireSandbox(ctx, conn, inv.OperationID, name)
	}
	jobCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	state, inspectErr := docker(jobCtx, "inspect", "--format", "{{.State.Status}}", name)
	if inspectErr != nil {

		if !strings.Contains(strings.ToLower(state), "no such") {
			return nil, fmt.Errorf("sandbox inspect failed: %s", truncateUTF8(state, 300))
		}

		out, e := docker(jobCtx, "create", "--init", "--name", name, "--network", "none", "--read-only", "--memory", "128m", "--cpus", "0.5", "--pids-limit", "64", "--cap-drop", "ALL", "--cap-add", "SETUID", "--cap-add", "SETGID", "--security-opt", "no-new-privileges", "--user", "0:0", "--tmpfs", "/tmp:rw,noexec,nosuid,size=16m", "--log-opt", "max-size=1m", "--log-opt", "max-file=1", "--label", "agentos.job="+inv.OperationID, "--entrypoint", "/usr/local/bin/agentos-sandbox-guard", image, args.Code)
		if e != nil {
			return nil, fmt.Errorf("sandbox create failed: %s", truncateUTF8(out, 300))
		}
		state = "created"
	}
	state = strings.TrimSpace(state)
	if state == "created" {
		if out, e := docker(jobCtx, "start", name); e != nil {
			return nil, fmt.Errorf("sandbox start failed: %s", truncateUTF8(out, 300))
		}
	}
	_, waitErr := docker(jobCtx, "wait", name)
	if waitErr != nil {
		cleanupCtx, c := context.WithTimeout(context.Background(), 5*time.Second)
		_, _ = docker(cleanupCtx, "kill", name)
		c()
		return nil, errors.New("sandbox interrupted; stop requested, durable job will be reconciled on retry or by reaper")
	}
	output, e := docker(jobCtx, "logs", name)
	if e != nil {
		return nil, e
	}
	exitCode, e := docker(jobCtx, "inspect", "--format", "{{.State.ExitCode}}", name)
	if e != nil {
		return nil, e
	}
	result = JSON(map[string]any{"stdout_and_stderr": output, "exit_code": strings.TrimSpace(exitCode), "output_capped_at_bytes": 65536, "container": name})
	if _, e = conn.Exec(ctx, "UPDATE sandbox_jobs SET status='completed',result=$2 WHERE id=$1", inv.OperationID, result); e != nil {
		return nil, e
	}

	cleanupSandbox(conn, inv.OperationID, name)
	return result, nil
}

// The independently deployed Sandbox needs only its own job table. Runtime
// remains the authority for leases, current constraints and bound tools.
func (s *Server) validateSandboxInvocation(ctx context.Context, inv Invocation) error {
	if inv.Name != "sandbox_exec" || !strings.HasPrefix(inv.OperationID, inv.RunID+"/") || inv.RunID == "" {
		return errors.New("invalid sandbox invocation")
	}
	if s.Store.Remote != nil {
		var result struct {
			Authorized bool `json:"authorized"`
		}
		e := s.Store.Remote.Request(ctx, "POST", s.Store.Remote.RuntimeURL+"/v1/sandbox/authorize", inv, &result)
		if e != nil {
			return e
		}
		if !result.Authorized {
			return errors.New("runtime did not authorize sandbox invocation")
		}
		return nil
	}
	return errors.New("runtime authorization client required")
}

func unlockSandbox(conn *pgxpool.Conn, id string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, e := conn.Exec(ctx, "SELECT pg_advisory_unlock(hashtextextended($1,21))", id); e != nil {
		_ = conn.Conn().Close(ctx)
	}
}

func cleanupSandbox(conn *pgxpool.Conn, id, name string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, e := docker(ctx, "rm", name)
	if e == nil || strings.Contains(strings.ToLower(out), "no such container") {
		_, _ = conn.Exec(ctx, "UPDATE sandbox_jobs SET cleaned_at=now() WHERE id=$1 AND status='completed'", id)
	}
}

func expireSandbox(ctx context.Context, conn *pgxpool.Conn, id, name string) ([]byte, error) {
	state, e := docker(ctx, "inspect", "--format", "{{.State.Status}}", name)
	output, exitCode := "", "not_started"
	if e != nil && !strings.Contains(strings.ToLower(state), "no such") {
		return nil, errors.New("cannot reconcile sandbox daemon state")
	}
	if e == nil {
		if strings.TrimSpace(state) == "running" {
			if _, e = docker(ctx, "kill", name); e != nil {
				return nil, errors.New("sandbox stop not confirmed")
			}
		}
		output, e = docker(ctx, "logs", name)
		if e != nil {
			return nil, e
		}
		exitCode, e = docker(ctx, "inspect", "--format", "{{.State.ExitCode}}", name)
		if e != nil {
			return nil, e
		}
	}
	result := JSON(map[string]any{"stdout_and_stderr": output, "exit_code": strings.TrimSpace(exitCode), "deadline_expired": true, "output_capped_at_bytes": 65536, "container": name})
	if _, e = conn.Exec(ctx, "UPDATE sandbox_jobs SET status='completed',result=$2 WHERE id=$1 AND status<>'completed'", id, result); e != nil {
		return nil, e
	}
	cleanupSandbox(conn, id, name)
	return result, nil
}

// ReapSandbox reconciles orphaned jobs across replicas. A container-side timeout
// bounds computation even if every broker dies. Logs remain until the receipt is
// durable; the reaper then removes only containers referenced by its job table.
func (s *Server) ReapSandbox(ctx context.Context) error {
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
			if !sandboxImageDigest.MatchString(os.Getenv("SANDBOX_IMAGE")) {
				continue
			}
			_ = s.reapSandboxBatch(ctx)
		}
	}
}

func (s *Server) reapSandboxBatch(ctx context.Context) error {
	rows, e := s.Store.DB.Query(ctx, "SELECT id FROM sandbox_jobs WHERE cleaned_at IS NULL AND created_at<now()-interval '35 seconds' ORDER BY created_at LIMIT 16")
	if e != nil {
		return e
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, id := range ids {
		jobCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
		conn, e := s.Store.DB.Acquire(jobCtx)
		if e != nil {
			cancel()
			return e
		}
		func() {
			defer conn.Release()
			var locked bool
			if e := conn.QueryRow(jobCtx, "SELECT pg_try_advisory_lock(hashtextextended($1,21))", id).Scan(&locked); e != nil || !locked {
				return
			}
			defer unlockSandbox(conn, id)
			var name, status string
			if e := conn.QueryRow(jobCtx, "SELECT container_name,status FROM sandbox_jobs WHERE id=$1 AND cleaned_at IS NULL", id).Scan(&name, &status); e != nil {
				return
			}
			if name != "agentos-"+Hash([]byte(id))[:32] {
				return
			}
			if status == "completed" {
				cleanupSandbox(conn, id, name)
			} else {
				_, _ = expireSandbox(jobCtx, conn, id, name)
			}
		}()
		cancel()
	}
	return nil
}
