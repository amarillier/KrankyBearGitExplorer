#!/bin/bash
# get-mesa.sh — check for a newer pal1000/mesa-dist-win release, download it, and
# stage the two DLLs the Windows OpenGL fallback needs (see
# internal/startup/mesa_fallback_windows.go) for manual testing. Never touches
# assets/mesa-win/ (the files actually bundled into the app) unless you pass
# --install, and even then only after you confirm you've tested on a real
# Windows host with no hardware OpenGL.
#
# Usage:
#   ./get-mesa.sh                 Check latest, download + stage it if newer
#   ./get-mesa.sh --check         Only report current vs. latest, no download
#   ./get-mesa.sh -v 26.2.0       Fetch a specific version instead of latest
#   ./get-mesa.sh --install       Stage, then (after confirmation) bundle into
#                                 assets/mesa-win/ and bump MESA_VERSION
#   ./get-mesa.sh --force         Re-download/re-stage even if already current
#   ./get-mesa.sh --zip           Also (re)build bin/mesa-fallback.zip — the
#                                 portable-build download for users with no
#                                 hardware OpenGL — from whatever is currently
#                                 in assets/mesa-win/. Combine with --install
#                                 to package the just-installed version;
#                                 standalone, it just repackages whatever's
#                                 already bundled.
#   ./get-mesa.sh --zip-out PATH  Override the zip's output path (default:
#                                 bin/mesa-fallback.zip)
#
# Requires: curl, jq, 7zz (or 7z/7za) on PATH; zip (or 7zz) for --zip.

set -euo pipefail

REPO="pal1000/mesa-dist-win"
ASSET_KIND="release-mingw"
NEEDED_FILES=("opengl32.dll" "libgallium_wgl.dll")

. "$(dirname "${BASH_SOURCE[0]}")/../lib.sh"   # moves to the project root and loads app.env, VERSION, hosts.env
SCRIPT_DIR="$ROOT"   # (older name for the project root, kept so the rest of this script is unchanged)
ASSETS_DIR="$SCRIPT_DIR/assets/mesa-win"
VERSION_FILE="$ASSETS_DIR/MESA_VERSION"
STAGE_ROOT="$SCRIPT_DIR/mesa-staging"
FORCE_MARKER_SAMPLES=(
  "$ASSETS_DIR/.force-mesa-fallback.sample"
  "$ASSETS_DIR/.force-mesa-compat.sample"
  "$ASSETS_DIR/.force-mesa-performance.sample"
)

VERSION=""   # the Mesa version to fetch (shadows the app VERSION from lib.sh on purpose)
CHECK_ONLY=0
DO_INSTALL=0
FORCE=0
DO_ZIP=0
ZIP_OUTPUT="$SCRIPT_DIR/bin/mesa-fallback.zip"

usage() { sed -n '2,26p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'; }

while [ $# -gt 0 ]; do
  case "$1" in
    -v|--version) VERSION="$2"; shift 2 ;;
    --check) CHECK_ONLY=1; shift ;;
    --install) DO_INSTALL=1; shift ;;
    --force) FORCE=1; shift ;;
    --zip) DO_ZIP=1; shift ;;
    --zip-out) ZIP_OUTPUT="$2"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "Unknown argument: $1" >&2; usage; exit 1 ;;
  esac
done

for bin in curl jq; do
  command -v "$bin" >/dev/null 2>&1 || { echo "error: $bin is required but not on PATH" >&2; exit 1; }
done
SEVENZ=""
for bin in 7zz 7z 7za; do
  if command -v "$bin" >/dev/null 2>&1; then SEVENZ="$bin"; break; fi
done
[ -n "$SEVENZ" ] || { echo "error: need a 7z-capable tool on PATH (7zz, 7z, or 7za — try 'brew install sevenzip')" >&2; exit 1; }

if [ "$DO_ZIP" -eq 1 ] && ! command -v zip >/dev/null 2>&1 && [ -z "$SEVENZ" ]; then
  echo "error: --zip needs 'zip' (or 7zz/7z/7za) on PATH" >&2
  exit 1
fi

# build_zip packages whatever is CURRENTLY in assets/mesa-win/ into
# mesa-fallback.zip — a mesa-fallback/ folder at the zip's top level
# containing both DLLs plus the force-fallback marker sample, mirroring
# the installer's own staging into {app}\mesa-fallback (see the project's
# Inno Setup script, if any).
build_zip() {
  for f in "${NEEDED_FILES[@]}"; do
    [ -f "$ASSETS_DIR/$f" ] || { echo "error: --zip: $ASSETS_DIR/$f missing — nothing bundled yet to zip up" >&2; exit 1; }
  done

  local zip_stage="$STAGE_ROOT/zip-staging"
  rm -rf "$zip_stage"
  mkdir -p "$zip_stage/mesa-fallback"
  for f in "${NEEDED_FILES[@]}"; do
    cp "$ASSETS_DIR/$f" "$zip_stage/mesa-fallback/$f"
  done
  for sample in "${FORCE_MARKER_SAMPLES[@]}"; do
    [ -f "$sample" ] && cp "$sample" "$zip_stage/mesa-fallback/"
  done
  # MESA_VERSION travels too: it is not moved into the app directory at run time (only the two DLLs are), so it stays put
  # for the About window to read.
  [ -f "$VERSION_FILE" ] && cp "$VERSION_FILE" "$zip_stage/mesa-fallback/"
  # Attribution travels with the DLLs: the notice, Mesa's licence statement, and every licence text Mesa uses
  # (build/tools/fetch_mesa_licenses.sh writes them; they are tracked in git, the DLLs are not).
  for f in MESA-NOTICE.txt MESA-LICENSE.rst; do
    [ -f "$ASSETS_DIR/$f" ] && cp "$ASSETS_DIR/$f" "$zip_stage/mesa-fallback/"
  done
  [ -d "$ASSETS_DIR/licenses" ] && cp -R "$ASSETS_DIR/licenses" "$zip_stage/mesa-fallback/licenses"

  mkdir -p "$(dirname "$ZIP_OUTPUT")"
  rm -f "$ZIP_OUTPUT"
  if command -v zip >/dev/null 2>&1; then
    (cd "$zip_stage" && zip -rq "$ZIP_OUTPUT" mesa-fallback)
  else
    "$SEVENZ" a -tzip "$ZIP_OUTPUT" "$zip_stage/mesa-fallback" >/dev/null
  fi
  rm -rf "$zip_stage"

  local zip_size
  zip_size="$(stat -f%z "$ZIP_OUTPUT" 2>/dev/null || stat -c%s "$ZIP_OUTPUT")"
  echo ""
  echo "Built $ZIP_OUTPUT ($zip_size bytes) from the currently bundled ${CURRENT_VERSION:-<unknown>} DLLs."
  echo "Attach this alongside the installer on the GitHub release for portable-build users."
}

echo "Mesa3D Windows OpenGL fallback — update checker"
echo "================================================"
echo ""

CURRENT_VERSION=""
[ -f "$VERSION_FILE" ] && CURRENT_VERSION="$(cat "$VERSION_FILE")"
echo "Currently bundled: ${CURRENT_VERSION:-<none>}"

if [ -z "$VERSION" ]; then
  echo "Checking latest release on GitHub ($REPO)..."
  VERSION="$(curl -sf --max-time 15 "https://api.github.com/repos/$REPO/releases/latest" | jq -r '.tag_name')"
  [ -n "$VERSION" ] && [ "$VERSION" != "null" ] || { echo "error: could not determine latest release (rate-limited? network down?)" >&2; exit 1; }
fi
echo "Target version:    $VERSION"
echo ""

if [ "$VERSION" = "$CURRENT_VERSION" ] && [ "$FORCE" -eq 0 ]; then
  echo "Already up to date — nothing to do. Pass --force to re-download anyway."
  [ "$DO_ZIP" -eq 1 ] && build_zip
  exit 0
fi

if [ "$CHECK_ONLY" -eq 1 ]; then
  if [ "$VERSION" != "$CURRENT_VERSION" ]; then
    echo "A newer version is available: $CURRENT_VERSION -> $VERSION"
  fi
  exit 0
fi

ASSET_NAME="mesa3d-${VERSION}-${ASSET_KIND}.7z"
DOWNLOAD_URL="https://github.com/$REPO/releases/download/${VERSION}/${ASSET_NAME}"
STAGE_DIR="$STAGE_ROOT/$VERSION"
ARCHIVE_PATH="$STAGE_ROOT/$ASSET_NAME"

mkdir -p "$STAGE_DIR"

if [ -f "$ARCHIVE_PATH" ] && [ "$FORCE" -eq 0 ]; then
  echo "Archive already downloaded: $ARCHIVE_PATH (skipping download; use --force to re-fetch)"
else
  echo "Downloading $ASSET_NAME ..."
  if ! curl -fL --max-time 300 --progress-bar -o "$ARCHIVE_PATH" "$DOWNLOAD_URL"; then
    rm -f "$ARCHIVE_PATH"
    echo "error: download failed — is '$ASSET_NAME' the right asset name for $VERSION?" >&2
    echo "       check https://github.com/$REPO/releases/tag/$VERSION" >&2
    exit 1
  fi
fi

echo ""
echo "Extracting x64 DLLs to $STAGE_DIR ..."
extract_args=()
for f in "${NEEDED_FILES[@]}"; do
  extract_args+=("x64/$f")
done
"$SEVENZ" e -o"$STAGE_DIR" -y "$ARCHIVE_PATH" "${extract_args[@]}" >/dev/null

echo ""
echo "Staged files:"
for f in "${NEEDED_FILES[@]}"; do
  staged="$STAGE_DIR/$f"
  [ -f "$staged" ] || { echo "error: expected $f not found in archive (layout changed upstream?)" >&2; exit 1; }
  size_staged="$(stat -f%z "$staged" 2>/dev/null || stat -c%s "$staged")"
  current="$ASSETS_DIR/$f"
  if [ -f "$current" ]; then
    size_current="$(stat -f%z "$current" 2>/dev/null || stat -c%s "$current")"
    printf "  %-20s %10s bytes  (currently bundled: %s bytes)\n" "$f" "$size_staged" "$size_current"
  else
    printf "  %-20s %10s bytes  (no version currently bundled)\n" "$f" "$size_staged"
  fi
done

echo ""
if [ "$DO_INSTALL" -eq 0 ]; then
  echo "Not bundled yet. Next steps:"
  echo "  1. Copy the two files from $STAGE_DIR onto a real Windows host with no"
  echo "     hardware OpenGL (or one where you've dropped a .force-mesa-fallback"
  echo "     marker — see internal/startup/mesa_fallback_windows.go) and confirm"
  echo "     the app still launches and renders correctly."
  echo "  2. Re-run this script with --install to bundle it into assets/mesa-win/"
  echo "     and bump MESA_VERSION, once you're satisfied."
  [ "$DO_ZIP" -eq 1 ] && { echo ""; echo "--zip was passed but nothing changed in assets/mesa-win/ yet — skipping (nothing new to package)."; }
  exit 0
fi

echo "You're about to overwrite assets/mesa-win/ (currently: ${CURRENT_VERSION:-<none>}) with $VERSION."
read -r -p "Have you already tested these DLLs on a real Windows host with no hardware OpenGL? [y/N] " reply
case "$reply" in
  [yY]|[yY][eE][sS]) ;;
  *) echo "Not installing. Staged files remain at $STAGE_DIR for testing."; exit 0 ;;
esac

mkdir -p "$ASSETS_DIR"
for f in "${NEEDED_FILES[@]}"; do
  cp "$STAGE_DIR/$f" "$ASSETS_DIR/$f"
done
printf '%s' "$VERSION" > "$VERSION_FILE"

# Refresh the licence texts and notice for this version (they ship beside the DLLs).
if [ -x "$SCRIPT_DIR/build/tools/fetch_mesa_licenses.sh" ]; then
  "$SCRIPT_DIR/build/tools/fetch_mesa_licenses.sh" "$VERSION" || echo "WARNING: could not fetch the Mesa licence texts: run build/tools/fetch_mesa_licenses.sh before shipping" >&2
fi

echo ""
echo "Installed $VERSION into $ASSETS_DIR"
echo "MESA_VERSION updated: ${CURRENT_VERSION:-<none>} -> $VERSION"
echo "Don't forget to rebuild the Windows installer and re-verify before shipping."
CURRENT_VERSION="$VERSION"

[ "$DO_ZIP" -eq 1 ] && build_zip
