package upstreams

import (
	"io"
	"net/http"
	"strings"

	"github.com/pkg/errors"
)

type PypiUpstream struct {
}

func (u PypiUpstream) Type() string {
	return "pypi"
}

func (u PypiUpstream) ModifyRequest(req *http.Request) error {
	req.Header.Set("Content-Type", "text/html")

	return nil
}

func (u PypiUpstream) ModifyResponse(resp *http.Response) (io.ReadCloser, error) {
	if resp.Header.Get("Content-Type") == "text/html" {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, errors.Wrap(err, "failed to read response body")
		}
		rewritten := strings.ReplaceAll(string(body), "https://files.pythonhosted.org/", "/pythonhosted/")

		return io.NopCloser(strings.NewReader(rewritten)), nil
	}

	return resp.Body, nil
}
