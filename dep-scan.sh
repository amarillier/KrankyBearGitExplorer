#!/usr/bin/env bash
# dep-scan — orchestrate dependency vulnerability scans across multiple
# project trees and aggregate findings into a single markdown report.
#
# Scanners used:
#   osv-scanner   — multi-ecosystem (Go, npm, pip, Cargo, Maven, …)
#   govulncheck   — Go-specific, call-graph aware (lower false-positive noise)
#
# See ~/.claude/skills/dep-scan/README.md for install + usage.

set -euo pipefail

# --- defaults -----------------------------------------------------------------

output=""
include_info=false
no_govulncheck=false
max_depth=4
prune_dirs=(node_modules vendor .git target build dist __pycache__ .venv venv .tox .next .pnpm-store)
roots_file="${HOME}/.claude/skills/dep-scan/roots.txt"

# --- helpers ------------------------------------------------------------------

usage() {
  cat <<'EOF'
dep-scan — multi-ecosystem dependency vulnerability scanner

Usage:
  dep-scan.sh [opts] [<path> ...]

Options:
  -o, --output FILE       Write markdown report to FILE (default: stdout).
      --include-info      Include LOW/INFO severity (default: MEDIUM+).
      --no-govulncheck    Skip per-Go-project govulncheck pass (faster).
      --max-depth N       Project discovery depth under each root (default: 4).
  -h, --help              Show this help.

If no paths are given, dep-scan reads ~/.claude/skills/dep-scan/roots.txt
(one path per line; lines starting with # are ignored).

Required tools:
  osv-scanner, jq        (govulncheck is optional but recommended for Go)
EOF
}

err() { printf 'dep-scan: %s\n' "$*" >&2; }

need_tool() {
  command -v "$1" >/dev/null 2>&1 || { err "required tool '$1' not found in PATH (see README install)"; return 1; }
}

# Build a `find` prune-expression for the configured directory names.
prune_expr() {
  local first=true
  local args=()
  for d in "${prune_dirs[@]}"; do
    if $first; then
      args+=(-name "$d"); first=false
    else
      args+=(-o -name "$d")
    fi
  done
  printf -- '-type d \\( '
  printf -- '%s ' "${args[@]}"
  printf -- '\\) -prune'
}

# --- argument parsing ---------------------------------------------------------

paths=()
while [[ $# -gt 0 ]]; do
  case "$1" in
    -o|--output)         output="$2"; shift 2 ;;
    --include-info)      include_info=true; shift ;;
    --no-govulncheck)    no_govulncheck=true; shift ;;
    --max-depth)         max_depth="$2"; shift 2 ;;
    -h|--help)           usage; exit 0 ;;
    --)                  shift; while [[ $# -gt 0 ]]; do paths+=("$1"); shift; done; break ;;
    -*)                  err "unknown option: $1"; usage >&2; exit 2 ;;
    *)                   paths+=("$1"); shift ;;
  esac
done

# If no paths on CLI, fall back to roots.txt.
if [[ ${#paths[@]} -eq 0 && -f "$roots_file" ]]; then
  while IFS= read -r line; do
    line="${line%%#*}"
    line="${line#"${line%%[![:space:]]*}"}"
    line="${line%"${line##*[![:space:]]}"}"
    [[ -z "$line" ]] && continue
    paths+=("$(eval echo "$line")")
  done <"$roots_file"
fi

if [[ ${#paths[@]} -eq 0 ]]; then
  err "no paths specified and no roots.txt found"
  usage >&2
  exit 2
fi

# --- tool checks --------------------------------------------------------------

need_tool jq           || exit 1
need_tool osv-scanner  || exit 1
have_govulncheck=true
if $no_govulncheck; then
  have_govulncheck=false
elif ! command -v govulncheck >/dev/null 2>&1; then
  have_govulncheck=false
  err "warning: govulncheck not found — Go projects will be scanned by osv-scanner only"
fi

# --- workspace ---------------------------------------------------------------

tmpdir=$(mktemp -d -t depscan.XXXXXXXX)
trap 'rm -rf "$tmpdir"' EXIT

osv_results=()  # JSON files from osv-scanner per root
gov_results=()  # JSON files from govulncheck per Go project (lines)
go_projects=()  # discovered Go projects (for govulncheck)

# --- discover projects + run scanners ----------------------------------------

idx=0
for root in "${paths[@]}"; do
  if [[ ! -d "$root" ]]; then
    err "warning: not a directory: $root"
    continue
  fi
  root_abs="$(cd "$root" && pwd)"

  # osv-scanner: one recursive pass per root. Picks up every supported manifest
  # under the root in a single invocation (faster than per-project).
  osv_out="$tmpdir/osv-$idx.json"
  if osv-scanner scan source --recursive --format=json "$root_abs" >"$osv_out" 2>"$tmpdir/osv-$idx.err"; then
    :
  else
    rc=$?
    # osv-scanner exits non-zero when vulns are found (rc=1). That's expected.
    # Other rc values (e.g. config errors) we surface.
    if [[ "$rc" -ne 1 ]]; then
      err "osv-scanner failed on $root_abs (rc=$rc) — see $tmpdir/osv-$idx.err"
    fi
  fi
  osv_results+=("$osv_out")

  # govulncheck: per Go module under this root.
  if $have_govulncheck; then
    # Discover go.mod files (excluding pruned dirs).
    while IFS= read -r mod; do
      go_projects+=("$(dirname "$mod")")
    done < <(eval "find \"$root_abs\" -maxdepth $max_depth $(prune_expr) -o -type f -name go.mod -print")
  fi

  idx=$((idx + 1))
done

if $have_govulncheck; then
  # Dedup — portable across macOS's stock bash 3.2 (no mapfile/readarray).
  if [[ ${#go_projects[@]} -gt 0 ]]; then
    _deduped=()
    while IFS= read -r _line; do
      [[ -n "$_line" ]] && _deduped+=("$_line")
    done < <(printf '%s\n' "${go_projects[@]}" | sort -u)
    go_projects=("${_deduped[@]}")
    unset _deduped _line
  fi
  gidx=0
  for gp in "${go_projects[@]}"; do
    gov_out="$tmpdir/gov-$gidx.json"
    (
      cd "$gp" || exit 0
      # govulncheck streams JSON objects (newline-delimited). Don't fail the
      # whole run when one project can't build; just record an empty file.
      govulncheck -format=json ./... >"$gov_out" 2>"$tmpdir/gov-$gidx.err" || true
    )
    gov_results+=("$gov_out|$gp")
    gidx=$((gidx + 1))
  done
fi

# --- aggregate ---------------------------------------------------------------

# Build a unified findings array as JSON:
# [
#   {
#     "source": "...",
#     "ecosystem": "...",
#     "package": "...",
#     "version": "...",
#     "vuln": "GHSA-... | CVE-...",
#     "summary": "...",
#     "severity": "CRITICAL|HIGH|MEDIUM|LOW|UNKNOWN",
#     "called": true,        # only set by govulncheck
#     "fix": "1.2.3"         # optional
#   }
# ]
findings_json="$tmpdir/findings.json"
printf '[]' >"$findings_json"

# Append findings from each osv-scanner JSON file.
for f in "${osv_results[@]}"; do
  [[ -s "$f" ]] || continue
  jq --slurpfile prev "$findings_json" '
    ($prev[0] // []) as $base
    | [ .results[]?
        | .source.path as $src
        | .packages[]?
        | .package as $pkg
        | .vulnerabilities[]?
        | {
            source: $src,
            ecosystem: ($pkg.ecosystem // "unknown"),
            package: ($pkg.name // "unknown"),
            version: ($pkg.version // "unknown"),
            vuln: (.id // (.aliases[0]? // "unknown")),
            summary: (.summary // ""),
            severity: (
              (.database_specific.severity? // "")
              | ascii_upcase
              | if . == "" then "UNKNOWN" else . end
            ),
            fix: (
              [ .affected[]?.ranges[]?.events[]? | select(.fixed) | .fixed ]
              | sort | last // ""
            ),
            scanner: "osv-scanner"
          }
      ]
    | $base + .
  ' "$f" >"$tmpdir/findings.next" && mv "$tmpdir/findings.next" "$findings_json"
done

# Append findings from each govulncheck JSON (newline-delimited objects).
for entry in "${gov_results[@]}"; do
  gf="${entry%%|*}"
  gp="${entry##*|}"
  [[ -s "$gf" ]] || continue
  # govulncheck emits a series of objects; we want the ones with an
  # "osv" field (the vuln description) and look at "finding" objects to know
  # whether the package is actually called.
  # Build a map id -> called=true based on finding objects with a trace.
  called_map="$tmpdir/called-$RANDOM.json"
  jq -s '
    [ .[] | select(.finding?) | .finding
      | select(.trace? and (.trace | length) > 0)
      | (.osv // "")
    ] | unique | map(select(. != "")) | reduce .[] as $id ({}; .[$id] = true)
  ' "$gf" >"$called_map" 2>/dev/null || printf '{}' >"$called_map"

  jq --slurpfile prev "$findings_json" --slurpfile callmap "$called_map" --arg src "$gp/go.mod" '
    ($prev[0] // []) as $base
    | ($callmap[0] // {}) as $called
    | [ inputs | select(.osv?) | .osv
        | {
            source: $src,
            ecosystem: "Go",
            package: ( [.affected[]?.package.name] | unique | join(", ") ),
            version: "",
            vuln: (.id // "unknown"),
            summary: (.summary // ""),
            severity: (
              ( [.severity[]?.score? // empty] | first // "" )
              | tostring | if . == "" then "UNKNOWN" else "CVSS:" + . end
            ),
            fix: "",
            called: ($called[.id] // false),
            scanner: "govulncheck"
          }
      ]
    | $base + .
  ' "$gf" >"$tmpdir/findings.next" 2>/dev/null && mv "$tmpdir/findings.next" "$findings_json" || true
done

# --- filter by severity ------------------------------------------------------

if ! $include_info; then
  jq '[ .[] | select(
        (.severity | ascii_upcase) as $s
        | $s == "CRITICAL" or $s == "HIGH" or $s == "MEDIUM" or ($s | startswith("CVSS"))
      ) ]' "$findings_json" >"$tmpdir/findings.filtered" && mv "$tmpdir/findings.filtered" "$findings_json"
fi

# --- emit markdown report ----------------------------------------------------

total=$(jq 'length' "$findings_json")
projects_scanned=$(printf '%s\n' "${paths[@]}")
ts=$(date '+%Y-%m-%d %H:%M:%S %Z')

# Severity order for sorting (sentinel string compares deterministically).
sev_rank() { case "$1" in CRITICAL) echo 0;; HIGH) echo 1;; MEDIUM) echo 2;; LOW) echo 3;; *) echo 4;; esac; }

{
  echo "# Dependency Vulnerability Report"
  echo
  echo "_Generated ${ts}_"
  echo
  echo "## Roots scanned"
  echo
  while IFS= read -r p; do echo "- \`$p\`"; done <<<"$projects_scanned"
  echo
  echo "## Summary"
  echo
  echo "- Total findings (after severity filter): **$total**"
  if [[ "$total" -eq 0 ]]; then
    echo
    echo "No vulnerabilities at or above the configured severity threshold. ✅"
    exit 0
  fi

  echo
  echo "### By severity"
  echo
  echo "| Severity | Count |"
  echo "|---|---|"
  for s in CRITICAL HIGH MEDIUM LOW UNKNOWN; do
    c=$(jq --arg s "$s" '[.[] | select((.severity | ascii_upcase) == $s or (.severity == "UNKNOWN" and $s == "UNKNOWN"))] | length' "$findings_json")
    [[ "$c" -gt 0 ]] && echo "| $s | $c |"
  done
  # CVSS-prefixed (govulncheck) findings shown separately to be honest about
  # the source.
  cvssc=$(jq '[.[] | select(.severity | startswith("CVSS"))] | length' "$findings_json")
  if [[ "$cvssc" -gt 0 ]]; then
    echo "| CVSS-rated (govulncheck) | $cvssc |"
  fi

  echo
  echo "### By package (action items)"
  echo
  echo "Each vulnerable package listed once, with all manifest locations and the"
  echo "suggested upgrade command. For Go and npm the command should be run inside"
  echo "each project directory; for transitive Go deps, follow with \`go mod tidy\`."
  echo
  jq -r '
    group_by(.package + "|" + .ecosystem)
    | map({
        package: .[0].package,
        ecosystem: .[0].ecosystem,
        sources: ([.[].source] | unique),
        fix: ([.[].fix | select(. != "")] | sort | last) ,
        top: (
          [.[].severity | ascii_upcase]
          | map(
              if . == "CRITICAL" then 0
              elif . == "HIGH" then 1
              elif . == "MEDIUM" then 2
              elif . == "LOW" then 3
              elif startswith("CVSS") then 2
              else 4 end
            )
          | min
        )
      })
    | sort_by(.top, .package)
    | .[]
    | (
        "- **\(.package)** (\(.ecosystem)) — " +
        (if .top == 0 then "CRITICAL"
         elif .top == 1 then "HIGH"
         elif .top == 2 then "MEDIUM"
         elif .top == 3 then "LOW"
         else "UNKNOWN" end) +
        (if (.fix // "") != "" then ", fixed in `\(.fix)`" else "" end) +
        "\n  - Suggested: `" +
        (
          if .ecosystem == "Go" then
            "go get \(.package)@" + (if (.fix // "") != "" then .fix else "latest" end)
          elif (.ecosystem | ascii_downcase) == "npm" then
            "npm install \(.package)@" + (if (.fix // "") != "" then .fix else "latest" end)
          elif .ecosystem == "PyPI" then
            "pip install -U \"\(.package)" + (if (.fix // "") != "" then ">=\(.fix)" else "" end) + "\""
          elif .ecosystem == "crates.io" then
            "cargo update -p \(.package)"
          elif .ecosystem == "Maven" then
            "(update \(.package) in pom.xml / build.gradle)"
          elif .ecosystem == "RubyGems" then
            "bundle update \(.package)"
          elif .ecosystem == "NuGet" then
            "dotnet add package \(.package)"
          else
            "(upgrade \(.package) manually)"
          end
        ) + "`" +
        (if .ecosystem == "Go" or (.ecosystem | ascii_downcase) == "npm" then " — run in each project directory below" else "" end) +
        "\n  - Found in:\n" +
        (.sources | map("    - `\(.)`") | join("\n"))
      )
  ' "$findings_json"

  echo
  echo "### By project"
  echo
  echo "| Project | Findings | Highest severity |"
  echo "|---|---|---|"
  # Group by source path. Show count + highest severity.
  jq -r '
    group_by(.source)
    | map({
        source: .[0].source,
        count: length,
        top: (
          [.[].severity | ascii_upcase]
          | map(
              if . == "CRITICAL" then 0
              elif . == "HIGH" then 1
              elif . == "MEDIUM" then 2
              elif . == "LOW" then 3
              elif startswith("CVSS") then 2
              else 4 end
            )
          | min
        )
      })
    | sort_by(.top)
    | .[]
    | "| `\(.source)` | \(.count) | \(if .top == 0 then "CRITICAL" elif .top == 1 then "HIGH" elif .top == 2 then "MEDIUM" elif .top == 3 then "LOW" else "UNKNOWN" end) |"
  ' "$findings_json"

  echo
  echo "## Findings"
  echo
  # Per-project sections, sorted by highest severity.
  jq -r '
    group_by(.source)
    | map({
        source: .[0].source,
        items: .,
        top: (
          [.[].severity | ascii_upcase]
          | map(
              if . == "CRITICAL" then 0
              elif . == "HIGH" then 1
              elif . == "MEDIUM" then 2
              elif . == "LOW" then 3
              elif startswith("CVSS") then 2
              else 4 end
            )
          | min
        )
      })
    | sort_by(.top)
    | .[]
    | "### \(.source)\n\n" +
      (.items
       | sort_by(if (.severity | ascii_upcase) == "CRITICAL" then 0
                 elif (.severity | ascii_upcase) == "HIGH" then 1
                 elif (.severity | ascii_upcase) == "MEDIUM" then 2
                 elif (.severity | ascii_upcase) == "LOW" then 3
                 elif (.severity | startswith("CVSS")) then 2
                 else 4 end)
       | map(
           "- **\(.vuln)** (\(.severity)" +
           (if .called then ", **CALLED**" else "" end) +
           ") — `\(.package)`" +
           (if .version != "" then "@\(.version)" else "" end) +
           (if .fix != "" then ", fixed in `\(.fix)`" else "" end) +
           " · _\(.summary // "")_ · via \(.scanner)"
         )
       | join("\n"))
  ' "$findings_json"

  echo
  echo "---"
  echo
  echo "_Generated by [dep-scan](~/.claude/skills/dep-scan/) using \`osv-scanner\`$( $have_govulncheck && echo " and \`govulncheck\`")._"
} | if [[ -n "$output" ]]; then tee "$output" >/dev/null; else cat; fi
