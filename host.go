package main

import "runtime"

// goos returns the pkgx OS slug for the current build.
func goos() string {
	switch runtime.GOOS {
	case "darwin":
		return "darwin"
	default:
		return "linux"
	}
}

// goarch returns the pkgx architecture slug for the current build.
func goarch() string {
	switch runtime.GOARCH {
	case "arm64":
		return "aarch64"
	case "amd64":
		return "x86-64"
	default:
		return runtime.GOARCH
	}
}
