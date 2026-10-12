package sandbox

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/euler-gaozl/agent-os-sdk/serviceclient"
	"github.com/jackc/pgx/v5/pgxpool"
)

// A real subprocess is killed by the parent test. Unlike a cancelled context,
// SIGKILL cannot run Go defers or request Docker cleanup.
func TestSandboxBrokerChild(t *testing.T) {
	dsn := os.Getenv("SANDBOX_TEST_CHILD_DSN")
	if dsn == "" {
		t.Skip("subprocess only")
	}
	var inv Invocation
	if e := json.Unmarshal([]byte(os.Getenv("SANDBOX_TEST_CHILD_INV")), &inv); e != nil {
		t.Fatal(e)
	}
	db, e := pgxpool.New(context.Background(), dsn)
	if e != nil {
		t.Fatal("open child database")
	}
	defer db.Close()
	s := &Store{DB: db}
	if endpoint := os.Getenv("SANDBOX_TEST_RUNTIME_URL"); endpoint != "" {
		s.Remote = &serviceclient.Client{RuntimeURL: endpoint, Token: os.Getenv("SANDBOX_TEST_RUNTIME_TOKEN")}
	}
	if _, e = (&Server{Store: s}).sandbox(context.Background(), inv); e != nil {
		t.Fatal(e)
	}
}
func TestDockerSandboxIsolationReplayAndBrokerCrash(t *testing.T) {
	if os.Getenv("TEST_DOCKER_SANDBOX") != "1" {
		t.Skip("isolated Linux Docker daemon required")
	}
	if !sandboxImageDigest.MatchString(os.Getenv("SANDBOX_IMAGE")) {
		t.Fatal("pinned test image required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	runID := ID()
	inv := Invocation{RunID: runID, OperationID: runID + "/tool/isolation", Owner: "sandbox-acceptance", Fence: 1, Name: "sandbox_exec"}
	inv.Arguments = JSON(map[string]string{"code": `const fs=require('fs'),net=require('net'),os=require('os');let root='';try{fs.writeFileSync('/forbidden','x');root='writable'}catch(e){root=e.code}const cap=fs.readFileSync('/proc/self/status','utf8').match(/CapEff:\s*(\w+)/)[1];const socket=net.connect({host:'1.1.1.1',port:443});socket.setTimeout(500);socket.on('error',e=>console.log(JSON.stringify({uid:process.getuid(),root,cap,network:e.code,interfaces:Object.keys(os.networkInterfaces())})));socket.on('timeout',()=>{socket.destroy();console.log('network timed out')});`})
	crash := inv
	crash.OperationID = runID + "/tool/crash"
	crash.Arguments = JSON(map[string]string{"code": `console.log('BROKER_CRASH_RECEIPT');for(const signal of ['SIGSTOP','SIGKILL']){try{process.kill(process.ppid,signal);console.log('GUARD_SIGNAL_ALLOWED')}catch(e){console.log('GUARD_SIGNAL_DENIED:'+signal+':'+e.code)}}while(true){}`})
	s, dsn, runtimeURL := sandboxDockerFixture(t, inv, crash)
	server := &Server{Store: s}
	body, e := server.sandbox(ctx, inv)
	if e != nil {
		t.Fatal(e)
	}
	var result struct {
		Output string `json:"stdout_and_stderr"`
		Exit   string `json:"exit_code"`
	}
	if e = json.Unmarshal(body, &result); e != nil {
		t.Fatal(e)
	}
	if result.Exit != "0" || !strings.Contains(result.Output, `"uid":65534`) || !strings.Contains(result.Output, `"cap":"0000000000000000"`) || (!strings.Contains(result.Output, "EROFS") && !strings.Contains(result.Output, "EACCES")) || !strings.Contains(result.Output, "ENETUNREACH") {
		t.Fatal("sandbox restrictions not observed", result)
	}
	again, e := server.sandbox(ctx, inv)
	if e != nil || !sameSandboxJSON(again, body) {
		t.Fatal("sandbox replay changed", e)
	}
	var count int
	if e = s.DB.QueryRow(ctx, "SELECT count(*) FROM sandbox_jobs WHERE cleaned_at IS NOT NULL").Scan(&count); e != nil || count != 1 {
		t.Fatal("receipt cleanup missing", e)
	}
	inv = crash
	child := exec.Command(os.Args[0], "-test.run=^TestSandboxBrokerChild$")
	child.Env = append(os.Environ(), "SANDBOX_TEST_CHILD_DSN="+dsn, "SANDBOX_TEST_CHILD_INV="+string(JSON(inv)), "SANDBOX_TEST_RUNTIME_URL="+runtimeURL, "SANDBOX_TEST_RUNTIME_TOKEN="+s.Remote.Token)
	if e = child.Start(); e != nil {
		t.Fatal(e)
	}
	waited := false
	name := "agentos-" + Hash([]byte(inv.OperationID))[:32]
	defer func() {
		// Stop the producer before removing its container, including when an
		// assertion fails while the broker is still creating/starting it.
		if !waited {
			_ = child.Process.Kill()
			_ = child.Wait()
		}
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if output, err := docker(cleanup, "rm", "-f", name); err != nil && !strings.Contains(output, "No such container") {
			t.Errorf("fixture container cleanup failed: %v: %s", err, output)
		}
	}()
	waitState := func(want string, seconds int) {
		t.Helper()
		until := time.Now().Add(time.Duration(seconds) * time.Second)
		for time.Now().Before(until) {
			state, e := docker(ctx, "inspect", "--format", "{{.State.Status}}", name)
			if e == nil && strings.TrimSpace(state) == want {
				return
			}
			time.Sleep(200 * time.Millisecond)
		}
		t.Fatal("container state timeout", want)
	}
	waitState("running", 15)
	inspect, e := docker(ctx, "inspect", name)
	if e != nil {
		t.Fatal(e)
	}
	var configs []struct {
		HostConfig struct {
			Init           bool
			NetworkMode    string
			ReadonlyRootfs bool
			Memory         int64
			NanoCpus       int64
			PidsLimit      int64
			CapDrop        []string
			CapAdd         []string
		}
		Config struct {
			User       string
			Cmd        []string
			Entrypoint []string
		}
	}
	if e = json.Unmarshal([]byte(inspect), &configs); e != nil || len(configs) != 1 {
		t.Fatal("container inspection failed")
	}
	c := configs[0]
	capabilities := map[string]bool{}
	for _, cap := range c.HostConfig.CapAdd {
		capabilities[strings.TrimPrefix(strings.ToUpper(cap), "CAP_")] = true
	}
	if !c.HostConfig.Init || c.HostConfig.NetworkMode != "none" || !c.HostConfig.ReadonlyRootfs || c.HostConfig.Memory != 128<<20 || c.HostConfig.NanoCpus != 500000000 || c.HostConfig.PidsLimit != 64 || c.Config.User != "0:0" || len(c.Config.Entrypoint) != 1 || c.Config.Entrypoint[0] != "/usr/local/bin/agentos-sandbox-guard" || len(c.HostConfig.CapAdd) != 2 || !capabilities["SETUID"] || !capabilities["SETGID"] {
		t.Fatal("sandbox resource config incorrect", c)
	}
	if e = child.Process.Kill(); e != nil {
		t.Fatal(e)
	}
	waitErr := child.Wait()
	waited = true
	if waitErr == nil || !brokerExitedBySIGKILL(child.ProcessState) {
		t.Fatal("broker did not exit by SIGKILL", waitErr, child.ProcessState)
	}
	// No broker exists now: the container must stop on its own within 30 seconds.
	waitState("exited", 40)
	if _, e = s.DB.Exec(ctx, "UPDATE sandbox_jobs SET created_at=now()-interval '1 minute' WHERE id=$1", inv.OperationID); e != nil {
		t.Fatal(e)
	}
	if e = server.reapSandboxBatch(ctx); e != nil {
		t.Fatal(e)
	}
	var saved []byte
	var cleaned bool
	if e = s.DB.QueryRow(ctx, "SELECT result,cleaned_at IS NOT NULL FROM sandbox_jobs WHERE id=$1", inv.OperationID).Scan(&saved, &cleaned); e != nil || !cleaned || !strings.Contains(string(saved), "BROKER_CRASH_RECEIPT") {
		t.Fatal("orphan result not durably recovered", e)
	}
	if !strings.Contains(string(saved), "GUARD_SIGNAL_DENIED:SIGSTOP:EPERM") || !strings.Contains(string(saved), "GUARD_SIGNAL_DENIED:SIGKILL:EPERM") || strings.Contains(string(saved), "GUARD_SIGNAL_ALLOWED") {
		t.Fatal("untrusted workload could signal trusted deadline guard")
	}
	restored, e := server.sandbox(ctx, inv)
	if e != nil || !sameSandboxJSON(restored, saved) {
		t.Fatal("orphan retry did not replay stored result", e)
	}
	t.Log("real container restrictions, replay, broker SIGKILL, independent timeout, orphan receipt recovery and cleanup passed")
}
func sameSandboxJSON(a, b []byte) bool {
	var x, y any
	return json.Unmarshal(a, &x) == nil && json.Unmarshal(b, &y) == nil && reflect.DeepEqual(x, y)
}
