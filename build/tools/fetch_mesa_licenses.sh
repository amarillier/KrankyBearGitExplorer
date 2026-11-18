#!/usr/bin/env bash
# Fetch the licence texts that go with the bundled Mesa3D software-OpenGL DLLs and write the notice that ships beside them:
#
#   assets/mesa-win/MESA-NOTICE.txt          what the DLLs are, where they come from, which licences apply
#   assets/mesa-win/MESA-LICENSE.rst         Mesa's own licence statement for that version (docs/license.rst)
#   assets/mesa-win/licenses/                every licence text Mesa uses (its licenses/ directory) at that version's tag,
#                                            plus the licence of the mesa-dist-win packaging (mesa-dist-win-LICENSE)
#
# The DLLs (assets/mesa-win/*.dll, from pal1000/mesa-dist-win) are not in git, but these small text files ARE tracked and
# are shipped in the installer and the portable zip so the attribution travels with the binaries. Run it after bumping
# MESA_VERSION (get-mesa.sh --install does that for you):
#
#   build/tools/fetch_mesa_licenses.sh [version]     version defaults to assets/mesa-win/MESA_VERSION (e.g. 26.2.4)
#
# Requires: curl, jq. The texts come from the Mesa tag "mesa-<version>" on gitlab.freedesktop.org, so they match the code
# the DLLs were built from.
set -euo pipefail
. "$(dirname "${BASH_SOURCE[0]}")/../lib.sh"   # moves to the project root and loads app.env, VERSION, hosts.env

ASSETS="assets/mesa-win"
VER="${1:-$(tr -d '[:space:]' < "$ASSETS/MESA_VERSION")}"
[ -n "$VER" ] || { echo "no version given and $ASSETS/MESA_VERSION is empty" >&2; exit 1; }
for tool in curl jq; do command -v "$tool" >/dev/null || { echo "$tool is required" >&2; exit 1; }; done

TAG="mesa-$VER"
API="https://gitlab.freedesktop.org/api/v4/projects/mesa%2Fmesa/repository"
RAW="https://gitlab.freedesktop.org/mesa/mesa/-/raw/$TAG"
OUT="$ASSETS/licenses"

echo "Mesa $VER: fetching licence texts from tag $TAG"
tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT

# every file under licenses/ (recursive, so the exceptions/ folder comes too)
curl -sf -m 30 "$API/tree?path=licenses&ref=$TAG&recursive=true&per_page=100" -o "$tmp/tree.json" \
    || { echo "cannot read the licenses/ list for tag $TAG (no network, or the tag does not exist)" >&2; exit 1; }
files="$(jq -r '.[] | select(.type=="blob") | .path' "$tmp/tree.json")"
[ -n "$files" ] || { echo "tag $TAG has no licenses/ files" >&2; exit 1; }

rm -rf "$OUT"; mkdir -p "$OUT"
while IFS= read -r f; do
    dest="$OUT/${f#licenses/}"
    mkdir -p "$(dirname "$dest")"
    curl -sf -m 30 "$RAW/$f" -o "$dest" || { echo "failed to fetch $f" >&2; exit 1; }
done <<< "$files"
curl -sf -m 30 "$RAW/docs/license.rst" -o "$ASSETS/MESA-LICENSE.rst" || { echo "failed to fetch docs/license.rst" >&2; exit 1; }
curl -sf -m 30 "https://raw.githubusercontent.com/pal1000/mesa-dist-win/master/LICENSE" -o "$OUT/mesa-dist-win-LICENSE" \
    || { echo "failed to fetch the mesa-dist-win LICENSE" >&2; exit 1; }

# llvmpipe's JIT is LLVM, built into libgallium_wgl.dll: name its version when the DLL is here to read it from.
LLVM_VER=""
if [ -f "$ASSETS/libgallium_wgl.dll" ]; then
    LLVM_VER="$(strings -a "$ASSETS/libgallium_wgl.dll" 2>/dev/null | sed -n 's/^llvm (version \([0-9.]*\)).*/\1/p' | head -1 || true)"
fi

cat > "$ASSETS/MESA-NOTICE.txt" <<NOTICE
Mesa3D software OpenGL renderer
================================

This folder holds opengl32.dll and libgallium_wgl.dll: Mesa 3D $VER, built for Windows by the mesa-dist-win project. They
are used only when this computer has no working hardware OpenGL, as a software renderer so the program can still draw its
window. On a computer with a working GPU they are not used.

$(printf ' * %-15s %s' "Mesa 3D" "https://www.mesa3d.org/")
     source: https://gitlab.freedesktop.org/mesa/mesa/-/tree/$TAG
$(printf ' * %-15s %s' "mesa-dist-win" "https://github.com/pal1000/mesa-dist-win")
     the Windows build; MIT License, Copyright (c) pal1000
$(printf ' * %-15s %s' "LLVM${LLVM_VER:+ $LLVM_VER}" "https://llvm.org/")
     built into libgallium_wgl.dll for the llvmpipe renderer; Apache License 2.0 with LLVM Exceptions

Licences
--------
Mesa is licensed mainly under the MIT license, but Mesa is made of many components and individual files carry their own
licences. Mesa's statement is in MESA-LICENSE.rst, and every licence text Mesa uses is in the licenses folder next to this
file (including Apache-2.0, which covers LLVM, and the licence of the mesa-dist-win packaging). Mesa, OpenGL, Vulkan and
the other names are the property of their respective owners. This notice is provided in good faith as attribution; the
authoritative terms are those of each component, at the links above.
NOTICE
echo "wrote $ASSETS/MESA-NOTICE.txt, $ASSETS/MESA-LICENSE.rst and $(echo "$files" | wc -l | tr -d ' ') files in $OUT/ (+ mesa-dist-win-LICENSE)"
