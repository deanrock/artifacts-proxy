package proxy

import (
	"artifacts-proxy/pkg/config"
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/johannesboyne/gofakes3"
	"github.com/johannesboyne/gofakes3/backend/s3mem"
)

func TestProxyBehaviour(t *testing.T) {
	t.Parallel()

	backend := s3mem.New()
	faker := gofakes3.New(backend)
	ts := httptest.NewServer(faker.Server())
	defer ts.Close()

	cfg := &config.Config{
		Auth: config.ConfigAuth{
			Username: "user",
			Password: "pass",
		},
		S3: &config.ConfigS3{
			Region:    "us-east-1",
			Bucket:    "bucket",
			Endpoint:  ts.URL,
			AccessKey: "ACCESS_KEY",
			SecretKey: "SECRET_KEY",
		},
		Upstreams: map[string]config.ConfigUpstream{
			"npm": {
				Type:                 "npm",
				Path:                 "/npm",
				UpstreamURL:          "http://127.0.0.1:1234/npm/",
				AuthenticationHeader: new("Bearer token"),
				MetadataMaxAge:       "5m",
				ContentMaxAge:        "5m",
			},
		},
		CacheDir:        t.TempDir(),
		EnableUpstreams: true,
	}
	if err := config.CheckConfig(cfg); err != nil {
		panic(err)
	}

	client, err := NewS3Client(cfg.S3)
	if err != nil {
		panic(err)
	}
	_, err = client.CreateBucket(context.Background(), &s3.CreateBucketInput{
		Bucket: aws.String("bucket"),
	})
	if err != nil {
		panic(err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	done := make(chan error, 1)
	go func() {
		err := RunServer(listener, cfg)

		// Fail if error happens, since otherwise test will just hang.
		if !errors.Is(err, net.ErrClosed) {
			log.Fatal(err)
		}

		done <- err
	}()

	url := "http://" + listener.Addr().String() + "/npm/"
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("creating request: %v", err)
	}
	req.SetBasicAuth("user", "pass")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()

	log.Fatal(resp.Status)

	listener.Close()
	<-done
}

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
				MetadataMaxAge:       "5m",
				ContentMaxAge:        "5m",
			},
		},
		CacheDir:        t.TempDir(),
		EnableUpstreams: false,
	}
	if err := config.CheckConfig(cfg); err != nil {
		panic(err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	done := make(chan error, 1)
	go func() {
		err := RunServer(listener, cfg)

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
		{name: "npm upstream without slash suffix with auth", path: "/npm", authUser: "user", authPass: "pass", expectedStatus: http.StatusBadGateway},
		{name: "npm upstream without slash suffix without auth", path: "/npm", authUser: "", authPass: "", expectedStatus: http.StatusUnauthorized},
		{name: "npm upstream with random suffix", path: "/npmsomething", authUser: "user", authPass: "pass", expectedStatus: http.StatusNotFound},
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
