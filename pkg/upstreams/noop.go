package upstreams

import (
	"io"
	"net/http"
)

type NoopUpstream struct {
	typ string
}

func NewNoopUpstream(typ string) NoopUpstream {
	return NoopUpstream{
		typ: typ,
	}
}

func (u NoopUpstream) Type() string {
	return u.typ
}

func (u NoopUpstream) ModifyRequest(req *http.Request) error {
	return nil
}

func (u NoopUpstream) ModifyResponse(resp *http.Response) (io.ReadCloser, error) {
	return resp.Body, nil
}
