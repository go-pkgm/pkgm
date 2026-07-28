package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

// captureStdout runs f with os.Stdout redirected to a pipe and returns what it
// wrote (runShell wires the interpreter to os.Stdout at call time).
func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	f()
	_ = w.Close()
	os.Stdout = old
	b, _ := io.ReadAll(r)
	return string(b)
}

func TestIsShellInvocation(t *testing.T) {
	if !isShellInvocation("/bin/sh", nil) {
		t.Error("/bin/sh should be shell")
	}
	if !isShellInvocation("/usr/local/bin/pkgm", []string{"sh", "-c", "x"}) {
		t.Error("pkgm sh … should be shell")
	}
	if isShellInvocation("/usr/local/bin/pkgm", []string{"install", "x"}) {
		t.Error("pkgm install should NOT be shell")
	}
}

func TestRunShellCmodeAndArith(t *testing.T) {
	out := captureStdout(t, func() {
		if code := runShell([]string{"sh", "-c", "echo $((2+3))"}); code != 0 {
			t.Errorf("code=%d", code)
		}
	})
	if out != "5\n" {
		t.Errorf("arith out=%q", out)
	}
}

func TestRunShellCoreutils(t *testing.T) {
	out := captureStdout(t, func() {
		runShell([]string{"sh", "-c", `echo "$(dirname /a/b/c) $(basename /a/b/c.txt .txt)"`})
	})
	if out != "/a/b c\n" {
		t.Errorf("coreutils out=%q", out)
	}
}

func TestRunShellFileModeParams(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "s.sh")
	_ = os.WriteFile(script, []byte("echo \"$0|$1|$@\"\n"), 0o755)
	out := captureStdout(t, func() {
		runShell([]string{"sh", script, "--version", "B"})
	})
	want := script + "|--version|--version B\n"
	if out != want {
		t.Errorf("file-mode out=%q want %q", out, want)
	}
}

func TestRunShellErrors(t *testing.T) {
	if code := runShell([]string{"sh"}); code != 0 {
		t.Errorf("empty sh code=%d", code)
	}
	if code := runShell([]string{"sh", "/no/such/script"}); code != 127 {
		t.Errorf("missing script code=%d", code)
	}
	if code := runShell([]string{"sh", "-c", "exit 3"}); code != 3 {
		t.Errorf("exit-status code=%d", code)
	}
	if code := runShell([]string{"sh", "-c", "if"}); code != 2 {
		t.Errorf("parse-error code=%d", code)
	}
}

func TestLoaderName(t *testing.T) {
	if goarch() == "aarch64" && loaderName() != "ld-linux-aarch64.so.1" {
		t.Errorf("loaderName=%q", loaderName())
	}
	if goarch() == "x86-64" && loaderName() != "ld-linux-x86-64.so.2" {
		t.Errorf("loaderName=%q", loaderName())
	}
}
