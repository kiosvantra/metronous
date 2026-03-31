// Package version provides the version information for Metronous.
// The Version variable is set at build time via -ldflags.
package version

// Version is set at build time via -ldflags: -X github.com/kiosvantra/metronous/internal/version.Version={{ .Version }}
var Version = "0.9.0"
