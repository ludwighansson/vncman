# 0010. Publish releases with GoReleaser

- Status: Accepted
- Date: 2026-11-17

## Context

VNCMan is published on GitHub. [0008](0008-support-linux.md) settled which binaries to build (macOS and Linux, arm64 and amd64, without cgo) and left open how to publish them. Operators shouldn't need a Go toolchain to install VNCMan.

## Decision

We will publish releases with [GoReleaser](https://goreleaser.com), configured in `.goreleaser.yaml`.

- **Triggered by a tag.** Pushing a `vX.Y.Z` tag and running `goreleaser release` creates a GitHub release.
- **Binaries.** It builds `vncman` for macOS and Linux on amd64 and arm64, with `CGO_ENABLED=0` and `-trimpath`. The release version is embedded, so `vncman --version` reports it.
- **Archives.** Each binary ships in a `.tar.gz` with VNCMan's MIT license, the README, the ADRs and noVNC's license. noVNC is embedded in the binary and is MPL-2.0, so the license must travel with it.
- **Checksums.** A `checksums.txt` file covers every archive.
- **Tests first.** The release runs `go test ./...` before building.
- **Deferred.** A Homebrew tap, Linux packages (deb, rpm), macOS code signing and notarization, and a CI workflow that releases on tag are each separate later decisions.

## Consequences

- Anyone can install VNCMan by downloading one archive, without Go.
- A release is reproducible from a tag, and `goreleaser release --snapshot --clean` tests the whole release locally without publishing.
- macOS binaries are not signed. When one is downloaded with a browser, macOS quarantines it, and the user has to remove the quarantine (`xattr -d com.apple.quarantine vncman`) or allow it in System Settings. Downloads with `curl` are not quarantined.
- Publishing needs a GitHub token with permission to create releases, either locally or in CI.

## Alternatives considered

- **`go install` only.** It needs no release tooling, but every user needs a Go toolchain. It remains possible alongside releases.
- **A hand-written script with `gh release`.** It is possible, but it reimplements cross-compiling, archiving and checksums, which GoReleaser already does well.
- **Building releases in GitHub Actions without GoReleaser.** This means the same work spread over workflow YAML, which is harder to run and test locally.
