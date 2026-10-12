package sandbox

import (
	"encoding/json"
	"errors"

	"github.com/euler-gaozl/agent-os-sdk/servicehttp"
	"io"

	"net/http"
)

type Server struct {
	Store             *Store
	Token, RuntimeURL string
}

type handler func(http.ResponseWriter, *http.Request) error

func decode(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if e := d.Decode(dst); e != nil {
		return e
	}
	var extra any
	if e := d.Decode(&extra); e != io.EOF {
		return errors.New("expected exactly one JSON value")
	}
	return nil
}

func wrap(h handler) http.HandlerFunc {
	return servicehttp.Wrap(h)
}
