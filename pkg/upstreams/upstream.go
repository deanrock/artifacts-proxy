package upstreams

import (
	"io"
	"net/http"
)

type UpstreamType interface {
	Type() string
	ModifyRequest(req *http.Request) error
	ModifyResponse(resp *http.Response) (io.ReadCloser, error)
}
