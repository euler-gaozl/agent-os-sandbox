package sandbox

import (
	"github.com/euler-gaozl/agent-os-sdk/servicehttp"
	"net/http"
)

func (s *Server) SandboxHandler(c servicehttp.Config) http.Handler {
	return servicehttp.New(c, s.Store.DB.Ping, s.sandboxRoutes)
}
