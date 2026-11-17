#!/bin/sh
# Replace the noVNC copy embedded in VNCMan with a release from GitHub.
# Usage: scripts/update-novnc.sh v1.7.0
set -eu

version=${1:?usage: $0 <noVNC tag, for example v1.7.0>}
dest="$(cd "$(dirname "$0")/.." && pwd)/internal/console/novnc"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

curl -fsSL "https://github.com/novnc/noVNC/archive/refs/tags/$version.tar.gz" -o "$tmp/novnc.tar.gz"
mkdir "$tmp/src"
tar -xzf "$tmp/novnc.tar.gz" -C "$tmp/src" --strip-components=1

rm -rf "$dest"
mkdir -p "$dest"
# Only what the browser needs: the client page, its app and core modules,
# vendored dependencies, and the license files. Go module zips leave out
# vendor directories, so vendor/ is stored as third_party/ and served under
# its original name (see internal/console/console.go).
cp -R "$tmp/src/app" "$tmp/src/core" "$dest/"
cp -R "$tmp/src/vendor" "$dest/third_party"
cp "$tmp/src/vnc.html" "$tmp/src/package.json" "$tmp/src/defaults.json" \
   "$tmp/src/mandatory.json" "$tmp/src/LICENSE.txt" "$tmp/src/AUTHORS" "$dest/"
printf '%s\n' "$version" > "$dest/VERSION"

echo "noVNC $version is now in $dest"
