package main

import (
	"errors"
	"fmt"
	"github.com/Gentleman-Programming/engram/v3/internal/store"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	serveStopTimeout    = 10 * time.Second
	serveStopPollInterval = 100 * time.Millisecond
	serveStopKILLGrace  = 2 * time.Second
)

var errProcUnsupported = errors.New("process command line not readable on this platform")

// cmdServeStop stops the running `engram serve` process identified by the PID
// file published by cmdServe. It sends SIGTERM first (graceful drain) and
// escalates to SIGKILL only if the process does not exit in time.
func cmdServeStop(cfg store.Config) error {
	pidPath := filepath.Join(cfg.DataDir, "serve.pid")
	data, err := os.ReadFile(pidPath)
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("no running engram serve found: %s not found (start it first with `engram serve`)", pidPath)
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", pidPath, err)
	}
	pid := strings.TrimSpace(string(data))
	if pid == "" {
		os.Remove(pidPath)
		return fmt.Errorf("invalid PID in %s: file is empty", pidPath)
	}
	num, err := strconv.Atoi(pid)
	if err != nil {
		os.Remove(pidPath)
		return fmt.Errorf("invalid PID %q in %s", pid, pidPath)
	}

	if err := verifyEngramServeProcess(num); err != nil {
		os.Remove(pidPath)
		return err
	}

	log.Printf("[engram] stopping engram serve PID %d (SIGTERM)", num)
	if processGone(num) {
		os.Remove(pidPath)
		log.Printf("[engram] engram serve PID %d already gone", num)
		return nil
	}
	if err := syscall.Kill(num, syscall.SIGTERM); err != nil {
		if processGone(num) {
			os.Remove(pidPath)
			log.Printf("[engram] PID %d exited between verify and signal; nothing to stop", num)
			return nil
		}
		os.Remove(pidPath)
		return fmt.Errorf("send SIGTERM to PID %d: %w", num, err)
	}

	if err := waitForProcessExit(num, serveStopTimeout); err != nil {
		log.Printf("[engram] PID %d still running after %v; trying SIGKILL", num, serveStopTimeout)
		if err := syscall.Kill(num, syscall.SIGKILL); err != nil && !processGone(num) {
			os.Remove(pidPath)
			return fmt.Errorf("send SIGKILL to PID %d: %w", num, err)
		}
		if err := waitForProcessExit(num, serveStopKILLGrace); err != nil {
			os.Remove(pidPath)
			return fmt.Errorf("PID %d did not exit after SIGKILL: %w", num, err)
		}
	}

	os.Remove(pidPath)
	log.Printf("[engram] engram serve PID %d stopped", num)
	return nil
}

// verifyEngramServeProcess best-effort checks that pid is an `engram serve`
// process before it is signaled, protecting against stale or reused PIDs. On
// Linux it reads /proc/<pid>/cmdline and requires the executable basename to
// be "engram" with a "serve" argument. On other platforms it only confirms the
// process exists and trusts the PID file.
func verifyEngramServeProcess(pid int) error {
	cmdline, err := readProcessCmdline(pid)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("process %d no longer exists; removing stale PID file", pid)
		}
		// Non-Linux: /proc is unavailable. Trust the PID file.
		return nil
	}
	args := strings.SplitN(cmdline, "\x00", -1)
	if len(args) == 0 || args[0] == "" {
		return fmt.Errorf("process %d has an empty command line", pid)
	}
	exe := filepath.Base(args[0])
	if exe != "engram" {
		return fmt.Errorf("PID %d is not an engram process (it is %q); removing stale PID file", pid, exe)
	}
	for _, a := range args {
		if a == "serve" {
			return nil
		}
	}
	return fmt.Errorf("PID %d is an engram process but not running `serve`; removing stale PID file", pid)
}

// readProcessCmdline returns the null-separated command line of a Linux process.
func readProcessCmdline(pid int) (string, error) {
	if runtime.GOOS != "linux" {
		return "", errProcUnsupported
	}
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// processGone reports whether the process is no longer running. On Unix it
// first treats a zombie as gone (a zombie has no running code; its parent
// reaps it shortly) and then uses `kill -0`, which checks existence without
// sending a real signal.
func processGone(pid int) bool {
	if runtime.GOOS == "windows" {
		return windowsProcessGone(pid)
	}
	if isLinuxZombie(pid) {
		return true
	}
	cmd := exec.Command("kill", "-0", strconv.Itoa(pid))
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	return cmd.Run() != nil
}

// isLinuxZombie reports whether the Linux process is a zombie (state "Z").
// A zombie has no running code and is effectively stopped; its parent will
// reap it shortly.
func isLinuxZombie(pid int) bool {
	if runtime.GOOS != "linux" {
		return false
	}
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return false
	}
	fields := strings.Fields(string(data))
	if len(fields) < 3 {
		return false
	}
	return fields[2] == "Z"
}

func windowsProcessGone(pid int) bool {
	out, err := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid)).Output()
	if err != nil {
		// Could not determine; assume alive to be safe during the wait.
		return false
	}
	return strings.Contains(strings.ToUpper(string(out)), "NO PROCESS FOUND")
}

// waitForProcessExit waits until pid is gone or timeout elapses.
func waitForProcessExit(pid int, timeout time.Duration) error {
	ticker := time.NewTicker(serveStopPollInterval)
	defer ticker.Stop()
	deadline := time.Now().Add(timeout)
	for range ticker.C {
		if processGone(pid) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("process %d did not exit within %v", pid, timeout)
		}
	}
	return fmt.Errorf("process %d did not exit within %v", pid, timeout)
}
