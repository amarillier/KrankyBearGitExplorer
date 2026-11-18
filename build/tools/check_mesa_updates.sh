#!/usr/bin/env bash
# Check whether a newer Mesa3D Windows build is available from pal1000/mesa-dist-win
# (the OpenGL software-rendering fallback bundled for Windows hosts with no usable
# hardware OpenGL — see internal/startup/mesa_fallback_windows.go). The DLLs
# themselves are gitignored (assets/mesa-win/*), not a Go/npm/etc. dependency, so
# Dependabot has no visibility into them; this is the manual equivalent.
set -euo pipefail
. "$(dirname "${BASH_SOURCE[0]}")/../lib.sh"   # moves to the project root and loads app.env, VERSION, hosts.env

VERSION_FILE="assets/mesa-win/MESA_VERSION"
REPO="pal1000/mesa-dist-win"

if ! command -v jq &>/dev/null; then
  echo "check_mesa_updates: jq is required" >&2
  exit 1
fi

CURRENT="unknown"
if [[ -f "$VERSION_FILE" ]]; then
  CURRENT="$(tr -d '[:space:]' < "$VERSION_FILE")"
fi

RELEASE_JSON="$(curl -sf --max-time 15 "https://api.github.com/repos/$REPO/releases/latest")" || {
  echo "check_mesa_updates: failed to reach GitHub API for $REPO" >&2
  exit 1
}

LATEST="$(echo "$RELEASE_JSON" | jq -r '.tag_name')"
if [[ -z "$LATEST" || "$LATEST" == "null" ]]; then
  echo "check_mesa_updates: could not parse latest release tag" >&2
  exit 1
fi

echo "Current pinned Mesa version : $CURRENT"
echo "Latest mesa-dist-win release: $LATEST"

if [[ "$CURRENT" == "$LATEST" ]]; then
  echo "Mesa build is up to date."
  exit 0
fi

ASSET_URL="$(echo "$RELEASE_JSON" | jq -r '.assets[] | select(.name | endswith("-release-mingw.7z")) | .browser_download_url' | head -1)"

echo ""
echo "⚠️  A newer Mesa build is available: $LATEST (currently pinned: $CURRENT)"
echo "Release page: https://github.com/$REPO/releases/tag/$LATEST"
if [[ -n "$ASSET_URL" ]]; then
  echo "Download:     $ASSET_URL"
fi
echo ""
echo "To update: download the -release-mingw.7z asset, extract x64/opengl32.dll"
echo "and x64/libgallium_wgl.dll into assets/mesa-win/, then run:"
echo "  echo -n \"$LATEST\" > $VERSION_FILE"
exit 2
