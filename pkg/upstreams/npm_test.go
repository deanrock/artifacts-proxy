package upstreams

import (
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

const jsonContentType = "application/json"

func TestNpmModifyResponseHandlesTarballUrls(t *testing.T) {
	upstreamBodyBytes, err := os.ReadFile("../../resources/unit/npmPackage.json")
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
			name:        "should modify with JSON content type",
			contentType: jsonContentType,
			body:        upstreamBody,
			expected:    strings.ReplaceAll(upstreamBody, "https://registry.npmjs.org", "http://localhost:3000/npm"),
		},
		{
			name:        "should not modify",
			contentType: "application/gzip",
			body:        upstreamBody,
			expected:    upstreamBody,
		},
		{
			name:        "should not modify with invalid JSON",
			contentType: jsonContentType,
			body:        "invalid" + upstreamBody,
			expected:    "invalid" + upstreamBody,
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

			upstream := NewNpmUpstream("http://localhost:3000", "/npm", "https://registry.npmjs.org")
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
