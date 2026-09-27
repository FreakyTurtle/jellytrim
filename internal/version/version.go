// Package version holds build information set by the linker.
package version

import "fmt"

// Set at build time with -ldflags "-X ...". The defaults identify a local build.
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// String returns a one-line description of the build.
func String() string {
	return fmt.Sprintf("jellytrim %s (commit %s, built %s)", Version, Commit, Date)
}
