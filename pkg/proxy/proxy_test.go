package proxy

import (
	"artifacts-proxy/pkg/config"
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"testing"
)

func TestDefaultRouteAuthTable(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Auth: config.ConfigAuth{
			Username: "user",
			Password: "pass",
		},
		Upstreams: map[string]config.ConfigUpstream{
			"npm": {
				Type:                 "npm",
				Path:                 "/npm",
				UpstreamURL:          "http://127.0.0.1:1234/npm/",
				AuthenticationHeader: new("Bearer token"),
				MetadataMaxAge:       "5ms",
				ContentMaxAge:        "5xm",
			},
		},
		CacheDir:        t.TempDir(),
		EnableUpstreams: false,
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	done := make(chan error, 1)
	go func() {
		err := RunServer(context.Background(), listener, cfg)

		// Fail if error happens, since otherwise test will just hang.
		if !errors.Is(err, net.ErrClosed) {
			log.Fatal(err)
		}

		done <- err
	}()

	tests := []struct {
		name           string
		path           string
		authUser       string
		authPass       string
		expectedStatus int
	}{
		{name: "root with auth", path: "/", authUser: "user", authPass: "pass", expectedStatus: http.StatusNotFound},
		{name: "root without auth", path: "/", authUser: "", authPass: "", expectedStatus: http.StatusUnauthorized},
		{name: "unknown subpath without auth", path: "/something/", authUser: "", authPass: "", expectedStatus: http.StatusUnauthorized},
		{name: "npm upstream without auth", path: "/npm/", authUser: "", authPass: "", expectedStatus: http.StatusUnauthorized},
		{name: "npm upstream with auth", path: "/npm/", authUser: "user", authPass: "pass", expectedStatus: http.StatusBadGateway},
	}

	client := http.DefaultClient

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			url := "http://" + listener.Addr().String() + tc.path
			req, err := http.NewRequest(http.MethodGet, url, nil)
			if err != nil {
				t.Fatalf("creating request: %v", err)
			}
			if tc.authUser != "" || tc.authPass != "" {
				req.SetBasicAuth(tc.authUser, tc.authPass)
			}

			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("do request: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != tc.expectedStatus {
				t.Fatalf("%s: expected status %d, got %d", tc.name, tc.expectedStatus, resp.StatusCode)
			}
		})
	}

	listener.Close()
	<-done
}
