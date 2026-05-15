#!/usr/bin/env pwsh
# dep-scan.ps1 — PowerShell port of dep-scan.sh
#
# Orchestrate dependency vulnerability scans across multiple project trees and
# aggregate findings into a single markdown report. Works on PowerShell 7+
# (pwsh) on Windows, macOS, and Linux; Windows PowerShell 5.1 should also work.
#
# Scanners used:
#   osv-scanner   — multi-ecosystem (Go, npm, pip, Cargo, Maven, ...)
#   govulncheck   — Go-specific, call-graph aware (lower false-positive noise)
#
# See ~/.claude/skills/dep-scan/README.md for install + usage.

[CmdletBinding()]
param(
    [Alias('o')]
    [string]$Output,

    [switch]$IncludeInfo,

    [switch]$NoGovulncheck,

    [int]$MaxDepth = 4,

    [Alias('h')]
    [switch]$Help,

    [Parameter(Position = 0, ValueFromRemainingArguments = $true)]
    [string[]]$Paths
)

$ErrorActionPreference = 'Continue'
Set-StrictMode -Version 3.0

# --- defaults -----------------------------------------------------------------

$PruneDirs = @(
    'node_modules', 'vendor', '.git', 'target', 'build', 'dist',
    '__pycache__', '.venv', 'venv', '.tox', '.next', '.pnpm-store'
)
$RootsFile = Join-Path $HOME '.claude/skills/dep-scan/roots.txt'

# --- helpers ------------------------------------------------------------------

function Show-Usage {
    @'
dep-scan — multi-ecosystem dependency vulnerability scanner

Usage:
  dep-scan.ps1 [-Output FILE] [-IncludeInfo] [-NoGovulncheck] [-MaxDepth N] [<path> ...]

Options:
  -Output FILE       Write markdown report to FILE (default: stdout).
  -IncludeInfo       Include LOW/INFO severity (default: MEDIUM+).
  -NoGovulncheck     Skip per-Go-project govulncheck pass (faster).
  -MaxDepth N        Project discovery depth under each root (default: 4).
  -Help              Show this help.

If no paths are given, dep-scan reads ~/.claude/skills/dep-scan/roots.txt
(one path per line; lines starting with # are ignored).

Required tools:
  osv-scanner        (govulncheck is optional but recommended for Go)
'@ | Write-Host
}

function Write-Err {
    param([string]$Message)
    Write-Host -ForegroundColor Red "dep-scan: $Message"
}

function Test-Tool {
    param([string]$Name)
    return [bool](Get-Command $Name -ErrorAction SilentlyContinue)
}

function Expand-HomePath {
    param([string]$Path)
    if ([string]::IsNullOrWhiteSpace($Path)) { return $Path }
    if ($Path -eq '~' -or $Path.StartsWith('~/') -or $Path.StartsWith('~\')) {
        return Join-Path $HOME $Path.Substring(2)
    }
    return [Environment]::ExpandEnvironmentVariables($Path)
}

function Find-ProjectMarkers {
    param(
        [string]$Root,
        [string[]]$Names,
        [int]$Depth
    )
    # Get-ChildItem -Depth is supported in PS 5+. We then filter out any path
    # that descends into a pruned directory.
    $items = Get-ChildItem -Path $Root -Recurse -Depth $Depth -File -ErrorAction SilentlyContinue |
        Where-Object { $Names -contains $_.Name }
    if (-not $items) { return @() }
    $pruneSet = @{}
    foreach ($p in $PruneDirs) { $pruneSet[$p] = $true }
    $sep = [IO.Path]::DirectorySeparatorChar
    $alt = [IO.Path]::AltDirectorySeparatorChar
    $items | Where-Object {
        $segments = $_.FullName.Split(@($sep, $alt))
        $hit = $false
        foreach ($s in $segments) {
            if ($pruneSet.ContainsKey($s)) { $hit = $true; break }
        }
        -not $hit
    }
}

function Get-SevRank {
    param([string]$Sev)
    $u = "$Sev".ToUpper()
    if ($u -eq 'CRITICAL') { return 0 }
    if ($u -eq 'HIGH')     { return 1 }
    if ($u -eq 'MEDIUM')   { return 2 }
    if ($u.StartsWith('CVSS')) { return 2 }
    if ($u -eq 'LOW')      { return 3 }
    return 4
}

function Get-SevName {
    param([int]$Rank)
    switch ($Rank) {
        0 { return 'CRITICAL' }
        1 { return 'HIGH' }
        2 { return 'MEDIUM' }
        3 { return 'LOW' }
        default { return 'UNKNOWN' }
    }
}

# --- arg handling -------------------------------------------------------------

if ($Help) { Show-Usage; exit 0 }

if (-not $Paths -or $Paths.Count -eq 0) {
    if (Test-Path $RootsFile) {
        $collected = @()
        foreach ($line in Get-Content $RootsFile) {
            $clean = ($line -split '#', 2)[0].Trim()
            if ($clean) { $collected += (Expand-HomePath $clean) }
        }
        $Paths = $collected
    }
}

if (-not $Paths -or $Paths.Count -eq 0) {
    Write-Err 'no paths specified and no roots.txt found'
    Show-Usage
    exit 2
}

# --- tool checks --------------------------------------------------------------

if (-not (Test-Tool 'osv-scanner')) {
    Write-Err "required tool 'osv-scanner' not found in PATH (see README install)"
    exit 1
}
$HaveGovulncheck = $true
if ($NoGovulncheck) {
    $HaveGovulncheck = $false
} elseif (-not (Test-Tool 'govulncheck')) {
    $HaveGovulncheck = $false
    Write-Host -ForegroundColor Yellow 'dep-scan: warning: govulncheck not found — Go projects will be scanned by osv-scanner only'
}

# --- scan ---------------------------------------------------------------------

$Findings = [System.Collections.Generic.List[object]]::new()

foreach ($root in $Paths) {
    $expanded = Expand-HomePath $root
    if (-not (Test-Path $expanded -PathType Container)) {
        Write-Host -ForegroundColor Yellow "dep-scan: warning: not a directory: $root"
        continue
    }
    $rootAbs = (Resolve-Path $expanded).Path

    # osv-scanner: one recursive pass per root. Picks up every supported
    # manifest in a single invocation.
    $osvOutput = & osv-scanner scan source --recursive --format=json $rootAbs 2>$null
    $osvExit = $LASTEXITCODE
    # osv-scanner exits 1 when vulns are found — that's expected; only
    # non-{0,1} exit codes indicate a tool/config failure.
    if ($osvExit -ne 0 -and $osvExit -ne 1) {
        Write-Host -ForegroundColor Yellow "dep-scan: warning: osv-scanner failed on $rootAbs (exit=$osvExit)"
        continue
    }
    if ($osvOutput) {
        $osvJson = $null
        try {
            $osvJson = ($osvOutput -join "`n") | ConvertFrom-Json -ErrorAction Stop
        } catch {
            Write-Host -ForegroundColor Yellow "dep-scan: warning: could not parse osv-scanner output for $rootAbs"
        }
        if ($osvJson -and $osvJson.PSObject.Properties['results']) {
            foreach ($result in $osvJson.results) {
                $src = if ($result.source -and $result.source.PSObject.Properties['path']) { $result.source.path } else { $rootAbs }
                if (-not $result.PSObject.Properties['packages']) { continue }
                foreach ($pkgWrap in $result.packages) {
                    $pkg = $pkgWrap.package
                    if (-not $pkgWrap.PSObject.Properties['vulnerabilities']) { continue }
                    foreach ($vuln in $pkgWrap.vulnerabilities) {
                        $sev = 'UNKNOWN'
                        if ($vuln.PSObject.Properties['database_specific'] -and
                            $vuln.database_specific -and
                            $vuln.database_specific.PSObject.Properties['severity'] -and
                            $vuln.database_specific.severity) {
                            $sev = "$($vuln.database_specific.severity)".ToUpper()
                        }
                        $fix = ''
                        if ($vuln.PSObject.Properties['affected']) {
                            $fixes = @()
                            foreach ($a in $vuln.affected) {
                                if (-not $a.PSObject.Properties['ranges']) { continue }
                                foreach ($r in $a.ranges) {
                                    if (-not $r.PSObject.Properties['events']) { continue }
                                    foreach ($e in $r.events) {
                                        if ($e.PSObject.Properties['fixed'] -and $e.fixed) { $fixes += "$($e.fixed)" }
                                    }
                                }
                            }
                            if ($fixes.Count -gt 0) {
                                $fix = ($fixes | Sort-Object | Select-Object -Last 1)
                            }
                        }
                        $Findings.Add([PSCustomObject]@{
                            source    = $src
                            ecosystem = if ($pkg.PSObject.Properties['ecosystem']) { $pkg.ecosystem } else { 'unknown' }
                            package   = if ($pkg.PSObject.Properties['name']) { $pkg.name } else { 'unknown' }
                            version   = if ($pkg.PSObject.Properties['version']) { $pkg.version } else { 'unknown' }
                            vuln      = if ($vuln.PSObject.Properties['id']) { $vuln.id } else { 'unknown' }
                            summary   = if ($vuln.PSObject.Properties['summary']) { $vuln.summary } else { '' }
                            severity  = $sev
                            fix       = $fix
                            called    = $false
                            scanner   = 'osv-scanner'
                        }) | Out-Null
                    }
                }
            }
        }
    }
}

# govulncheck per Go module
if ($HaveGovulncheck) {
    $goProjects = @()
    foreach ($root in $Paths) {
        $expanded = Expand-HomePath $root
        if (-not (Test-Path $expanded -PathType Container)) { continue }
        $rootAbs = (Resolve-Path $expanded).Path
        $mods = Find-ProjectMarkers -Root $rootAbs -Names @('go.mod') -Depth $MaxDepth
        foreach ($m in $mods) { $goProjects += $m.Directory.FullName }
    }
    $goProjects = $goProjects | Sort-Object -Unique

    foreach ($gp in $goProjects) {
        Push-Location $gp
        try {
            $govRaw = & govulncheck -format=json ./... 2>$null
            # govulncheck emits a sequence of JSON objects (newline-delimited).
            $objs = @()
            $buf = New-Object System.Text.StringBuilder
            foreach ($line in @($govRaw)) {
                $null = $buf.AppendLine($line)
            }
            $text = $buf.ToString()
            # Parse line-by-line — each balanced object is its own JSON doc.
            # Some govulncheck versions emit a single JSON array. Try array
            # first, then fall back to per-line.
            try {
                $maybeArray = $text | ConvertFrom-Json -ErrorAction Stop
                if ($maybeArray -is [System.Array]) {
                    $objs = $maybeArray
                } else {
                    $objs = @($maybeArray)
                }
            } catch {
                foreach ($line in ($text -split "`n")) {
                    $t = $line.Trim()
                    if (-not $t) { continue }
                    try {
                        $objs += ($t | ConvertFrom-Json -ErrorAction Stop)
                    } catch {
                        # ignore non-JSON noise
                    }
                }
            }

            $called = @{}
            foreach ($o in $objs) {
                if ($o.PSObject.Properties['finding'] -and
                    $o.finding -and
                    $o.finding.PSObject.Properties['trace'] -and
                    $o.finding.trace -and
                    @($o.finding.trace).Count -gt 0 -and
                    $o.finding.PSObject.Properties['osv']) {
                    $called[$o.finding.osv] = $true
                }
            }
            foreach ($o in $objs) {
                if (-not ($o.PSObject.Properties['osv'])) { continue }
                $osv = $o.osv
                $pkgNames = @()
                if ($osv.PSObject.Properties['affected']) {
                    foreach ($a in $osv.affected) {
                        if ($a.PSObject.Properties['package'] -and $a.package.PSObject.Properties['name']) {
                            $pkgNames += $a.package.name
                        }
                    }
                }
                $pkgStr = ($pkgNames | Sort-Object -Unique) -join ', '
                $sev = 'UNKNOWN'
                if ($osv.PSObject.Properties['severity'] -and $osv.severity -and @($osv.severity).Count -gt 0) {
                    $first = $osv.severity[0]
                    if ($first.PSObject.Properties['score'] -and $first.score) {
                        $sev = "CVSS:$($first.score)"
                    }
                }
                $vulnId = if ($osv.PSObject.Properties['id']) { $osv.id } else { 'unknown' }
                $Findings.Add([PSCustomObject]@{
                    source    = (Join-Path $gp 'go.mod')
                    ecosystem = 'Go'
                    package   = $pkgStr
                    version   = ''
                    vuln      = $vulnId
                    summary   = if ($osv.PSObject.Properties['summary']) { $osv.summary } else { '' }
                    severity  = $sev
                    fix       = ''
                    called    = [bool]$called[$vulnId]
                    scanner   = 'govulncheck'
                }) | Out-Null
            }
        } finally {
            Pop-Location
        }
    }
}

# --- severity filter ----------------------------------------------------------

if (-not $IncludeInfo) {
    $Findings = [System.Collections.Generic.List[object]]::new(
        @($Findings | Where-Object {
            $u = "$($_.severity)".ToUpper()
            $u -eq 'CRITICAL' -or $u -eq 'HIGH' -or $u -eq 'MEDIUM' -or $u.StartsWith('CVSS')
        })
    )
}

# --- emit report --------------------------------------------------------------

$Ts = (Get-Date).ToString('yyyy-MM-dd HH:mm:ss zzz')
$Total = @($Findings).Count

$lines = @()
$lines += '# Dependency Vulnerability Report'
$lines += ''
$lines += "_Generated $Ts_"
$lines += ''
$lines += '## Roots scanned'
$lines += ''
foreach ($p in $Paths) { $lines += "- ``$p``" }
$lines += ''
$lines += '## Summary'
$lines += ''
$lines += "- Total findings (after severity filter): **$Total**"

if ($Total -eq 0) {
    $lines += ''
    $lines += 'No vulnerabilities at or above the configured severity threshold. ✅'
    $report = $lines -join "`n"
    if ($Output) { Set-Content -Path $Output -Value $report -Encoding utf8 } else { Write-Output $report }
    exit 0
}

$lines += ''
$lines += '### By severity'
$lines += ''
$lines += '| Severity | Count |'
$lines += '|---|---|'
foreach ($s in 'CRITICAL', 'HIGH', 'MEDIUM', 'LOW', 'UNKNOWN') {
    $c = @($Findings | Where-Object { "$($_.severity)".ToUpper() -eq $s }).Count
    if ($c -gt 0) { $lines += "| $s | $c |" }
}
$cvssC = @($Findings | Where-Object { "$($_.severity)".ToUpper().StartsWith('CVSS') }).Count
if ($cvssC -gt 0) { $lines += "| CVSS-rated (govulncheck) | $cvssC |" }

$lines += ''
$lines += '### By package (action items)'
$lines += ''
$lines += 'Each vulnerable package listed once, with all manifest locations and the'
$lines += 'suggested upgrade command. For Go and npm the command should be run inside'
$lines += 'each project directory; for transitive Go deps, follow with `go mod tidy`.'
$lines += ''

function Get-FixCommand {
    param([string]$Ecosystem, [string]$Package, [string]$Fix)
    $eco = "$Ecosystem"
    $ecoLower = $eco.ToLower()
    $ver = if ($Fix) { $Fix } else { 'latest' }
    switch -Regex ($eco) {
        '^Go$'        { return "go get $Package@$ver" }
    }
    if ($ecoLower -eq 'npm')       { return "npm install $Package@$ver" }
    if ($eco -eq 'PyPI')           {
        if ($Fix) { return "pip install -U `"$Package>=$Fix`"" }
        return "pip install -U `"$Package`""
    }
    if ($eco -eq 'crates.io')      { return "cargo update -p $Package" }
    if ($eco -eq 'Maven')          { return "(update $Package in pom.xml / build.gradle)" }
    if ($eco -eq 'RubyGems')       { return "bundle update $Package" }
    if ($eco -eq 'NuGet')          { return "dotnet add package $Package" }
    return "(upgrade $Package manually)"
}

$byPackage = $Findings | Group-Object { "$($_.package)|$($_.ecosystem)" } | ForEach-Object {
    $first = $_.Group[0]
    $fixes = @($_.Group | Where-Object { $_.fix } | ForEach-Object { $_.fix } | Sort-Object -Unique)
    $latestFix = if ($fixes.Count -gt 0) { $fixes[-1] } else { '' }
    $ranks = @($_.Group | ForEach-Object { Get-SevRank $_.severity })
    $top = ($ranks | Sort-Object | Select-Object -First 1)
    $srcs = @($_.Group | ForEach-Object { $_.source } | Sort-Object -Unique)
    [PSCustomObject]@{
        package   = $first.package
        ecosystem = $first.ecosystem
        fix       = $latestFix
        top       = $top
        topName   = (Get-SevName $top)
        sources   = $srcs
    }
} | Sort-Object top, package

foreach ($pkg in $byPackage) {
    $fixNote = if ($pkg.fix) { ", fixed in ``$($pkg.fix)``" } else { '' }
    $cmd = Get-FixCommand -Ecosystem $pkg.ecosystem -Package $pkg.package -Fix $pkg.fix
    $cmdNote = if ($pkg.ecosystem -eq 'Go' -or "$($pkg.ecosystem)".ToLower() -eq 'npm') { ' — run in each project directory below' } else { '' }
    $lines += "- **$($pkg.package)** ($($pkg.ecosystem)) — $($pkg.topName)$fixNote"
    $lines += "  - Suggested: ``$cmd``$cmdNote"
    $lines += '  - Found in:'
    foreach ($s in $pkg.sources) {
        $lines += "    - ``$s``"
    }
}

$lines += ''
$lines += '### By project'
$lines += ''
$lines += '| Project | Findings | Highest severity |'
$lines += '|---|---|---|'

$byProject = $Findings | Group-Object source | ForEach-Object {
    $ranks = @($_.Group | ForEach-Object { Get-SevRank $_.severity })
    $top = ($ranks | Sort-Object | Select-Object -First 1)
    [PSCustomObject]@{
        source = $_.Name
        count  = $_.Count
        top    = $top
        topName = (Get-SevName $top)
        items  = $_.Group
    }
} | Sort-Object top

foreach ($p in $byProject) {
    $lines += "| ``$($p.source)`` | $($p.count) | $($p.topName) |"
}

$lines += ''
$lines += '## Findings'
$lines += ''

foreach ($p in $byProject) {
    $lines += "### $($p.source)"
    $lines += ''
    $items = $p.items | Sort-Object { Get-SevRank $_.severity }
    foreach ($it in $items) {
        $calledFlag = if ($it.called) { ', **CALLED**' } else { '' }
        $versionStr = if ($it.version) { "@$($it.version)" } else { '' }
        $fixStr = if ($it.fix) { ", fixed in ``$($it.fix)``" } else { '' }
        $sum = "$($it.summary)"
        $lines += "- **$($it.vuln)** ($($it.severity)$calledFlag) — ``$($it.package)``$versionStr$fixStr · _$sum_ · via $($it.scanner)"
    }
    $lines += ''
}

$lines += '---'
$lines += ''
$govSuffix = if ($HaveGovulncheck) { ' and `govulncheck`' } else { '' }
$lines += "_Generated by [dep-scan](~/.claude/skills/dep-scan/) using ``osv-scanner``$govSuffix._"

$report = $lines -join "`n"
if ($Output) {
    Set-Content -Path $Output -Value $report -Encoding utf8
} else {
    Write-Output $report
}
