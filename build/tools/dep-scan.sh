#!/usr/bin/env bash
# Dependency vulnerability scan for this project. Uses the shared dep-scan tool (osv-scanner + govulncheck, one markdown report)
# from the Claude skill, so there is one copy to keep current instead of one per project; falls back to plain govulncheck.
#   build/tools/dep-scan.sh [extra dep-scan options]
. "$(dirname "${BASH_SOURCE[0]}")/../lib.sh"
SKILL="$HOME/.claude/skills/dep-scan/dep-scan.sh"
if [ -x "$SKILL" ]; then
    exec "$SKILL" "$@" "$ROOT"
elif command -v govulncheck >/dev/null; then
    echo "dep-scan skill not found: running govulncheck only"
    exec govulncheck ./...
fi
echo "neither the dep-scan skill nor govulncheck is installed (go install golang.org/x/vuln/cmd/govulncheck@latest)" >&2
exit 1
