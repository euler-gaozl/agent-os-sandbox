package sandbox

import (
	"encoding/json"

	"strings"
)

func invocationHash(inv Invocation) string {
	var arguments any
	decoder := json.NewDecoder(strings.NewReader(string(inv.Arguments)))
	decoder.UseNumber()
	if e := decoder.Decode(&arguments); e != nil {
		return Hash(inv.Arguments)
	}
	return Hash(JSON([]any{inv.Name, arguments}))
}
