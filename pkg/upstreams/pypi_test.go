package upstreams

import (
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

const htmlContentType = "text/html"

func TestPypiModifyRequest(t *testing.T) {
	req := http.Request{
		RequestURI: "/mock",
		Header:     http.Header{},
	}
	PypiUpstream{}.ModifyRequest(&req)

	assert.Equal(t, req.Header, http.Header{
		"Content-Type": {htmlContentType},
	})
}

func TestPypiModifyResponseHandlesTarballUrls(t *testing.T) {
	upstreamBodyBytes, err := os.ReadFile("../../resources/unit/pypiPackage.html")
	if err != nil {
		t.Fatal(err)
	}
	upstreamBody := string(upstreamBodyBytes)

	tests := []struct {
		name        string
		contentType string
		body        string
		expected    string
	}{
		{
			name:        "should modify with HTML content type",
			contentType: htmlContentType,
			body:        upstreamBody,
			expected:    strings.ReplaceAll(upstreamBody, "https://files.pythonhosted.org", "/pythonhosted"),
		},
		{
			name:        "should not modify",
			contentType: "application/gzip",
			body:        upstreamBody,
			expected:    upstreamBody,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp := http.Response{
				Body: io.NopCloser(strings.NewReader(tc.body)),
				Header: map[string][]string{
					"Content-Type": {tc.contentType},
				},
				Request: &http.Request{
					RequestURI: "/mock",
				},
			}

			upstream := PypiUpstream{}
			rc, err := upstream.ModifyResponse(&resp)
			if err != nil {
				t.Fatal(err)
			}

			body, err := io.ReadAll(rc)
			if err != nil {
				t.Fatal(err)
			}

			assert.Equal(t, tc.expected, string(body))
		})
	}
}
