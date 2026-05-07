// Package version exposes the running binary's build metadata.
//
// The variables are overridden at link time via -ldflags "-X ...". Default
// sentinel values mark a developer build, which the update checker treats as
// a signal to skip itself entirely.
package version

// Version is the SemVer of the running binary, e.g. "v0.1.0". Source builds
// default to "dev"; treat any reader of this value as needing to handle that
// case.
var Version = "dev"

// Commit is the short commit SHA the binary was built from, or "unknown" for
// source builds.
var Commit = "unknown"

// BuildDate is the build timestamp in RFC3339 form, or "unknown" for source
// builds.
var BuildDate = "unknown"
