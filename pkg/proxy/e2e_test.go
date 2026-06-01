//go:build e2e
// +build e2e

package proxy

import (
	"artifacts-proxy/pkg/config"
	"context"
	"fmt"
	"log"
	"net"
	"os/exec"
	"testing"
	"time"

	"github.com/pkg/errors"
)

func buildImage(t *testing.T, project string) {
	t.Helper()
	cmd := exec.Command("docker", "build", "-t", fmt.Sprintf("test-%s", project), ".")
	cmd.Dir = fmt.Sprintf("../../resources/projects/%s", project)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build %s failed: %v\n%s", project, err, out)
	}
}

func runCommand(t *testing.T, image string, port uint16, command []string) {
	t.Helper()
	// -i without -t: attach stdin but no pseudo-TTY (avoids pipe/TTY conflicts)
	// --add-host: on Linux, host.docker.internal is not resolved automatically (unlike Mac/Windows)
	args := []string{"run", "-i", "--rm", "--init", "--add-host=host.docker.internal:host-gateway", "-e", fmt.Sprintf("PORT=%d", port), image}
	args = append(args, command...)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run %s failed: %v\n%s", image, err, out)
	}
}

func testCachingBehaviour(t *testing.T, project string, command []string) {
	t.Helper()
	cacheDir := t.TempDir()

	runWithConfig := func(f func(config *config.Config)) {
		listener, err := net.Listen("tcp", "0.0.0.0:0")
		if err != nil {
			t.Fatal(err)
		}
		port := uint16(listener.Addr().(*net.TCPAddr).Port)

		config, err := config.ParseFile("../../config.toml")
		if err != nil {
			t.Fatal(err)
		}
		config.Port = port
		config.CacheDir = cacheDir
		f(config)

		done := make(chan struct{})
		go func() {
			defer close(done)

			err := RunServer(listener, config)

			// Fail if error happens, since otherwise test will just hang.
			if !errors.Is(err, net.ErrClosed) {
				log.Fatal(err)
			}

			done <- struct{}{}
		}()

		runCommand(t, project, port, command)

		listener.Close()
		<-done
	}

	// Phase 1: populate cache via upstream
	runWithConfig(func(config *config.Config) {})

	// Phase 2: serve from cache only
	runWithConfig(func(config *config.Config) {
		config.EnableUpstreams = false
	})
}

func TestNpmE2E(t *testing.T) {
	t.Parallel()
	buildImage(t, "npm")
	testCachingBehaviour(t, "test-npm", nil)
}

func TestMavenE2E(t *testing.T) {
	t.Parallel()
	buildImage(t, "maven")
	testCachingBehaviour(t, "test-maven", nil)
}

func TestAptE2E(t *testing.T) {
	t.Parallel()
	buildImage(t, "apt")
	testCachingBehaviour(t, "test-apt", nil)
}

func TestPypiE2E(t *testing.T) {
	t.Parallel()
	buildImage(t, "pypi")
	testCachingBehaviour(t, "test-pypi", nil)
}
