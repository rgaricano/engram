package main

import (
	"fmt"
	"github.com/Gentleman-Programming/engram/v3/internal/store"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func moduleRoot() string {
	d := ""
	if wd, err := os.Getwd(); err == nil {
		d = wd
	}
	for {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d
		}
		next := filepath.Dir(d)
		if next == d || next == "" || next == "/" {
			break
		}
		d = next
	}
	return ""
}

func waitHealthy(t *testing.T, url string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			if resp.StatusCode == http.StatusOK {
				resp.Body.Close()
				return
			}
			resp.Body.Close()
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("server not healthy at %s within %v", url, timeout)
}

// TestProcessGone verifies the existence probe against a real spawned process.
func TestProcessGone(t *testing.T) {
	cmd := exec.Command("sleep", "5")
	if err := cmd.Start(); err != nil {
		t.Fatalf("spawn sleep: %v", err)
	}
	pid := cmd.Process.Pid
	if processGone(pid) {
		cmd.Process.Kill()
		t.Fatal("processGone reported a live process is gone")
	}
	cmd.Process.Kill()
	cmd.Wait()
	if !processGone(pid) {
		t.Fatal("processGone failed to detect a dead process")
	}
}

// TestWaitForProcessExit verifies the bounded wait returns once the process dies.
func TestWaitForProcessExit(t *testing.T) {
	cmd := exec.Command("sleep", "1")
	if err := cmd.Start(); err != nil {
		t.Fatalf("spawn sleep: %v", err)
	}
	// Reap the child in a goroutine so it does not linger as a zombie; a
	// zombie still makes `kill -0` report the PID as alive.
	go func() {
		_ = cmd.Wait()
	}()
	if err := waitForProcessExit(cmd.Process.Pid, 5*time.Second); err != nil {
		t.Fatalf("waitForProcessExit: %v", err)
	}
}

// TestVerifyEngramServeProcess_RejectsNonEngram ensures a non-`engram serve`
// PID is rejected before it is signaled.
func TestVerifyEngramServeProcess_RejectsNonEngram(t *testing.T) {
	cmd := exec.Command("sleep", "5")
	if err := cmd.Start(); err != nil {
		t.Fatalf("spawn sleep: %v", err)
	}
	pid := cmd.Process.Pid
	defer func() {
		cmd.Process.Kill()
		_ = cmd.Wait()
	}()
	err := verifyEngramServeProcess(pid)
	if err == nil {
		t.Fatalf("verifyEngramServeProcess accepted a non-engram process")
	}
	t.Logf("rejected non-engram process: %v", err)
}

// TestCmdServeStop_PIDFileMissing exercises the no-server error path.
func TestCmdServeStop_PIDFileMissing(t *testing.T) {
	dataDir := t.TempDir()
	cfg := store.Config{DataDir: dataDir}
	err := cmdServeStop(cfg)
	if err == nil {
		t.Fatal("cmdServeStop succeeded with no PID file")
	}
	if !strings.Contains(err.Error(), "no running engram serve found") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestCmdServeStop_InvalidPID exercises an empty/invalid PID file.
func TestCmdServeStop_InvalidPID(t *testing.T) {
	for _, content := range []string{"", "abc", "99999999"} {
		t.Run("content="+content, func(t *testing.T) {
			dataDir := t.TempDir()
			pidPath := filepath.Join(dataDir, "serve.pid")
			if err := os.WriteFile(pidPath, []byte(content), 0600); err != nil {
				t.Fatalf("write PID file: %v", err)
			}
			cfg := store.Config{DataDir: dataDir}
			if content == "99999999" {
				// stale path: PID may be gone; accept either the "no longer exists"
				// or "did not exit" outcome as long as the file is cleaned up.
				_ = cmdServeStop(cfg)
			} else {
				err := cmdServeStop(cfg)
				if err == nil {
					t.Fatalf("cmdServeStop succeeded with invalid PID content %q", content)
				}
				if !strings.Contains(err.Error(), "invalid PID") {
					t.Fatalf("unexpected error: %v", err)
				}
			}
			if _, err := os.Stat(pidPath); err == nil {
				t.Errorf("PID file not removed after invalid content: %s", pidPath)
			}
		})
	}
}

// TestCmdServeStop_StalePID ensures a dead PID is rejected and the file cleaned up.
func TestCmdServeStop_StalePID(t *testing.T) {
	cmd := exec.Command("sleep", "1")
	if err := cmd.Start(); err != nil {
		t.Fatalf("spawn sleep: %v", err)
	}
	pid := cmd.Process.Pid
	cmd.Process.Kill()
	_ = cmd.Wait()

	dataDir := t.TempDir()
	pidPath := filepath.Join(dataDir, "serve.pid")
	if err := os.WriteFile(pidPath, []byte(fmt.Sprintf("%d", pid)), 0600); err != nil {
		t.Fatalf("write PID file: %v", err)
	}
	cfg := store.Config{DataDir: dataDir}
	err := cmdServeStop(cfg)
	if err == nil {
		t.Fatal("cmdServeStop succeeded for a dead PID")
	}
	if _, err := os.Stat(pidPath); err == nil {
		t.Errorf("PID file not removed after stale PID: %s", pidPath)
	}
	t.Logf("stale PID rejected: %v", err)
}

// TestServeStop_EndToEnd builds the real binary, starts a serve process, stops
// it with the CLI, and asserts graceful shutdown plus PID-file cleanup.
func TestServeStop_EndToEnd(t *testing.T) {
	dataDir := t.TempDir()
	port, err := freePort()
	if err != nil {
		t.Fatalf("free port: %v", err)
	}

	bin := filepath.Join(t.TempDir(), "engram")
	root := moduleRoot()
	if root == "" {
		t.Fatal("could not find go.mod to resolve module root")
	}
	build := exec.Command("go", "build", "-o", bin, "./cmd/engram")
	build.Dir = root
	var buildOut, buildErr strings.Builder
	build.Stdout, build.Stderr = &buildOut, &buildErr
	if err := build.Run(); err != nil {
		t.Fatalf("go build: %v\nout: %s\nerr: %s", err, buildOut.String(), buildErr.String())
	}

	// Dispatch/error path: `engram serve stop` with no running server.
	stopCmd := exec.Command(bin, "serve", "stop")
	stopCmd.Env = append(os.Environ(), "ENGRAM_DATA_DIR="+dataDir)
	var stopOut, stopErr strings.Builder
	stopCmd.Stdout, stopCmd.Stderr = &stopOut, &stopErr
	if err := stopCmd.Run(); err == nil {
		t.Fatal("expected `engram serve stop` to fail with no running server")
	}
	if !strings.Contains(stopOut.String()+stopErr.String(), "no running engram serve found") {
		t.Fatalf("unexpected serve stop output: %s", stopOut.String()+stopErr.String())
	}

	// Start a real serve process.
	serveCmd := exec.Command(bin, "serve")
	serveCmd.Env = append(os.Environ(),
		"ENGRAM_DATA_DIR="+dataDir,
		"ENGRAM_PORT="+fmt.Sprintf("%d", port),
	)
	serveLog, err := os.CreateTemp(t.TempDir(), "serve.log")
	if err != nil {
		t.Fatalf("create serve log: %v", err)
	}
	defer serveLog.Close()
	serveCmd.Stdout, serveCmd.Stderr = serveLog, serveLog
	if err := serveCmd.Start(); err != nil {
		t.Fatalf("start serve: %v", err)
	}

	url := fmt.Sprintf("http://127.0.0.1:%d/health", port)
	waitHealthy(t, url, 60*time.Second)

	// Stop it via the CLI.
	pidPath := filepath.Join(dataDir, "serve.pid")
	// The serve process should have written the PID file by now.
	if _, err := os.Stat(pidPath); err != nil {
		t.Fatalf("PID file missing before stop: %v (serve log: %s)", err, readServeLog(serveLog))
	}
	stopCmd2 := exec.Command(bin, "serve", "stop")
	stopCmd2.Env = append(os.Environ(), "ENGRAM_DATA_DIR="+dataDir)
	var stopOut2, stopErr2 strings.Builder
	stopCmd2.Stdout, stopCmd2.Stderr = &stopOut2, &stopErr2
	stopExit := stopCmd2.Run()
	stopOutput := strings.TrimSpace(stopOut2.String() + stopErr2.String())
	if stopOutput != "" {
		t.Logf("stop output: %s", stopOutput)
	}
	if stopExit != nil {
		t.Errorf("cmdServeStop returned non-zero: %v (stop output: %q, serve log: %s)", stopExit, stopOutput, readServeLog(serveLog))
	}
	if err := serveCmd.Wait(); err != nil {
		// A clean SIGTERM shutdown may surface as the process exiting via signal;
		// accept exit or signal termination as graceful shutdown.
		t.Fatalf("serve process did not exit cleanly: %v (log: %s)", err, readServeLog(serveLog))
	}

	if _, err := os.Stat(pidPath); err == nil {
		t.Errorf("PID file was not removed after stop: %s", pidPath)
	}
}

func readServeLog(f *os.File) string {
	f.Seek(0, 1)
	b := make([]byte, 4096)
	n, err := f.ReadAt(b, 0)
	if err != nil {
		return fmt.Sprintf("read log: %v", err)
	}
	return string(b[:n])
}
