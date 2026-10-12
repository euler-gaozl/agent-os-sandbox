package sandbox

// Authorize is this service's inbound policy. Peer authentication is supplied by the SDK.
func Authorize(caller, method, path string) bool {
	if path == "/readyz" || path == "/metrics" {
		return method == "GET" && (caller == "sandbox" || caller == "control")
	}

	return caller == "runtime" && method == "POST" && path == "/execute"

}
