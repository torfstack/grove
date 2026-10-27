#!/bin/sh
set -eu

if [ "$(uname -s)" != "Darwin" ]; then
  echo "Grove.app must be built on macOS" >&2
  exit 1
fi
repo=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
out=${1:-"$repo/bin/Grove.app"}
macos="$out/Contents/MacOS"
helpers="$out/Contents/Helpers"
mkdir -p "$macos" "$helpers" "$out/Contents/Resources/LaunchAgents"
(
  cd "$repo"
  go build -o "$macos/grove-tray" ./cmd/grove-tray
  go build -o "$helpers/grove" ./cmd/grove
  go build -o "$helpers/groved" ./cmd/groved
)
cp "$repo/contrib/macos/Grove-Info.plist" "$out/Contents/Info.plist"
cp "$repo/contrib/macos/launchagents/"*.plist.in "$out/Contents/Resources/LaunchAgents/"
