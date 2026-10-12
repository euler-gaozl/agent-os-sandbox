package sandbox

import (
	"context"
	"encoding/json"
	"github.com/euler-gaozl/agent-os-sdk/serviceclient"
	"github.com/euler-gaozl/agent-os-sdk/servicehttp"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOnlyRuntimeMayRequestExecution(t *testing.T) {
	token := strings.Repeat("fixture-", 5)
	for _, caller := range []string{"runtime", "control", "worker", "futures"} {
		h := servicehttp.New(servicehttp.Config{Role: "sandbox", Peers: map[string]string{caller: servicehttp.TokenHash(token)}, Authorize: Authorize}, nil, func(m *http.ServeMux) {
			m.HandleFunc("/execute", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
		})
		r := httptest.NewRequest("POST", "/execute", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		want := 403
		if caller == "runtime" {
			want = 204
		}
		if w.Code != want {
			t.Fatal(caller, w.Code)
		}
	}
}

func TestRuntimeAuthorizationIsRequiredWithoutExecutionTables(t *testing.T) {
	token := strings.Repeat("fixture-", 5)
	inv := Invocation{RunID: "fixture", OperationID: "fixture/step", Name: "sandbox_exec", Arguments: JSON(map[string]string{"code": "fixture"}), Fence: 9}
	allowed := false
	requests := 0
	runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/sandbox/authorize" || r.Method != "POST" || r.Header.Get("Authorization") != "Bearer "+token {
			t.Error("invalid runtime protocol")
		}
		var got Invocation
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil || got.Fence != inv.Fence || string(got.Arguments) != string(inv.Arguments) {
			t.Error("invocation identity lost")
		}
		requests++
		servicehttp.JSONResponse(w, 200, map[string]bool{"authorized": allowed})
	}))
	defer runtime.Close()
	server := &Server{Store: &Store{Remote: &serviceclient.Client{RuntimeURL: runtime.URL, Token: token}}}
	if err := server.validateSandboxInvocation(context.Background(), inv); err == nil {
		t.Fatal("runtime denial ignored")
	}
	allowed = true
	if err := server.validateSandboxInvocation(context.Background(), inv); err != nil {
		t.Fatal(err)
	}
	if requests != 2 {
		t.Fatal("authorization not checked")
	}
	server.Store.Remote = nil
	if err := server.validateSandboxInvocation(context.Background(), inv); err == nil {
		t.Fatal("missing authority accepted")
	}
	t.Setenv("SANDBOX_IMAGE", "")
	if _, err := server.sandbox(context.Background(), inv); err == nil || !strings.Contains(err.Error(), "host execution is disabled") {
		t.Fatal("missing isolation did not fail closed", err)
	}
}
