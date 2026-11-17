// Package platform holds the few things that differ between macOS and
// Linux (ADR 0008): where files live, how to open a URL, and how to find a
// viewer program.
package platform

// AppName names VNCMan's directories under the state and cache directories.
const AppName = "vncman"
