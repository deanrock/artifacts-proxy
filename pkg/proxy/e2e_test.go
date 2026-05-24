package proxy_test

import (
	"artifacts-proxy/pkg/config"
	"artifacts-proxy/pkg/proxy"
	"context"
	"fmt"
	"net"
	"os/exec"
	"testing"
	"time"
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

	// Phase 1: populate cache via upstream
	listener1, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	port1 := uint16(listener1.Addr().(*net.TCPAddr).Port)

	config1, err := config.ParseFile("../../config.toml")
	if err != nil {
		t.Fatal(err)
	}
	config1.Port = port1
	config1.CacheDir = cacheDir

	done1 := make(chan struct{})
	go func() {
		defer close(done1)
		proxy.RunServer(listener1, config1)
	}()

	runCommand(t, project, port1, command)

	listener1.Close()
	<-done1

	// Phase 2: serve from cache only
	listener2, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	port2 := uint16(listener2.Addr().(*net.TCPAddr).Port)

	config2, err := config.ParseFile("../../config.toml")
	if err != nil {
		t.Fatal(err)
	}
	config2.Port = port2
	config2.CacheDir = cacheDir

	go proxy.RunServer(listener2, config2)

	runCommand(t, project, port2, command)
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
