// Package version carries build metadata, stamped by the linker at build
// time (see the Makefile / .goreleaser.yaml -ldflags).
package version

import (
	"fmt"
	"runtime"
)

var (
	// Version is the semver tag or "dev".
	Version = "dev"
	// Commit is the short git SHA.
	Commit = "none"
	// Date is the build timestamp.
	Date = "unknown"
)

// String returns a one-line version banner including Go/OS/arch.
func String() string {
	return fmt.Sprintf("training %s (commit %s, built %s, %s %s/%s)",
		Version, Commit, Date, runtime.Version(), runtime.GOOS, runtime.GOARCH)
}
