# install.ps1 -- the one-line installer for dexel on Windows.
#
#   irm https://raw.githubusercontent.com/jawwadzafar/dexel/main/install.ps1 | iex
#   irm https://get.dexel.jwdlab.com/install.ps1 | iex          (later, same file)
#
# The Windows half of docs/production-runtime/RELEASE_PIPELINE.md section 6,
# stage 1: same ten steps as install.sh, same GitHub-API release resolution,
# same refusal to install anything whose sha256 does not match.
#
# Windows PowerShell 5.1 and PowerShell 7+ both. That means: no ternary
# `? :`, no `??`, no `-SkipHttpErrorCheck`, no `$PSStyle`, single-argument
# Join-Path chains, and TLS 1.2 forced on because 5.1 on an unpatched box
# still defaults to TLS 1.0 and GitHub refuses that.
#
# Every step lives in a function; the only top-level work is the `try` at
# the very bottom, so a truncated download cannot half-install anything.
#
# Never elevates. Writes only under %LOCALAPPDATA% and HKCU. Never enables
# autostart and never starts the runtime.
#
# Environment (a `| iex` one-liner cannot pass parameters, so everything is
# an environment variable):
#   $env:DEXEL_INSTALL_DIR   where dexel.exe goes (default %LOCALAPPDATA%\dexel\bin)
#   $env:DEXEL_VERSION       install this tag instead of the latest release
#   $env:DEXEL_REPO          resolve against a different repository
#   $env:DEXEL_ARCHIVE       use a .zip already on disk (checksum still verified)
#   $env:DEXEL_DRY_RUN       "1" = resolve + download + verify, then stop
#   $env:DEXEL_NO_PATH       "1" = do not touch the user PATH
#   $env:GH_TOKEN / $env:GITHUB_TOKEN
#                            bearer token; needed only while the repo is private
#   $env:DEXEL_ARCH_RAW      override the detected architecture string (testing)
#
# Exit codes match install.sh: 2 usage, 3 unsupported platform, 4 missing
# tool, 5 no build for this platform in this release, 6 checksum mismatch,
# 7 network/API failure, 8 the installed binary failed its version check.

Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'

# Invoke-WebRequest's progress bar makes a 5 MB download take minutes on
# Windows PowerShell 5.1. This is not cosmetic.
$script:OldProgress = $ProgressPreference
$ProgressPreference = 'SilentlyContinue'

$script:ExitCode = 0
$script:TempDir = $null
$script:Repo = 'jawwadzafar/dexel'
if ($env:DEXEL_REPO) { $script:Repo = $env:DEXEL_REPO }
$script:Api = "https://api.github.com/repos/$($script:Repo)"
$script:StoppedRuntime = $false

function Say([string]$Text)  { Write-Host $Text }
function Step([string]$Text) { Write-Host "==> $Text" }
function Note([string]$Text) { Write-Host "  $Text" }
function Warn([string]$Text) { Write-Warning $Text }

# Fail is the only failure path: it records the exit code the caller should
# see, then throws so the single try/catch at the bottom does the reporting
# and the cleanup in one place.
function Fail([int]$Code, [string]$Message) {
    $script:ExitCode = $Code
    throw $Message
}

# --------------------------------------------------------------------------
# step 1 -- platform and architecture
# --------------------------------------------------------------------------

function Test-OnWindows {
    # $IsWindows exists only on PowerShell 6+; on 5.1 the absence of it is
    # itself the answer, because 5.1 runs nowhere else.
    if (Test-Path 'Variable:IsWindows') { return [bool](Get-Variable -Name IsWindows -ValueOnly) }
    return $true
}

function Resolve-Platform {
    if (-not (Test-OnWindows)) {
        Fail 3 @"
this is not Windows. install.ps1 is the Windows installer; on Linux and macOS use:

  curl -fsSL https://raw.githubusercontent.com/$($script:Repo)/main/install.sh | bash
"@
    }

    if ($PSVersionTable.PSVersion.Major -lt 5) {
        Fail 4 "needs Windows PowerShell 5.1 or newer; this is $($PSVersionTable.PSVersion)."
    }

    # PROCESSOR_ARCHITEW6432 is set when a 32-bit PowerShell is running on a
    # 64-bit machine, and is the only variable that tells the truth then.
    $raw = $env:DEXEL_ARCH_RAW
    if (-not $raw) { $raw = $env:PROCESSOR_ARCHITEW6432 }
    if (-not $raw) { $raw = $env:PROCESSOR_ARCHITECTURE }

    switch ($raw) {
        'AMD64' { return 'amd64' }
        'x86_64' { return 'amd64' }
        'ARM64' { return 'arm64' }
        'x86'   {
            Fail 3 @"
32-bit x86 Windows is not supported. dexel publishes windows-amd64 and
windows-arm64 builds only.
Build from source instead: https://github.com/$($script:Repo)#building-from-source
"@
        }
        default {
            Fail 3 @"
unsupported architecture: $raw
dexel publishes windows-amd64 and windows-arm64 builds.
Build from source instead: https://github.com/$($script:Repo)#building-from-source
"@
        }
    }
}

# --------------------------------------------------------------------------
# step 2 -- HTTPS, once, for everything
# --------------------------------------------------------------------------

function Enable-Tls12 {
    try {
        $wanted = [Net.SecurityProtocolType]::Tls12
        # Keep TLS 1.3 if this runtime knows about it; do not *require* it.
        if ([enum]::GetNames([Net.SecurityProtocolType]) -contains 'Tls13') {
            $wanted = $wanted -bor [Net.SecurityProtocolType]::Tls13
        }
        [Net.ServicePointManager]::SecurityProtocol =
            [Net.ServicePointManager]::SecurityProtocol -bor $wanted
    } catch {
        Warn "could not raise the TLS version; the download may fail on an old box."
    }
}

function Get-DexelToken {
    if ($env:GH_TOKEN) { return $env:GH_TOKEN }
    if ($env:GITHUB_TOKEN) { return $env:GITHUB_TOKEN }
    return ''
}

function Get-Headers([string]$Accept) {
    $h = @{ 'Accept' = $Accept; 'User-Agent' = 'dexel-install.ps1' }
    $t = Get-DexelToken
    if ($t) {
        $h['Authorization'] = "Bearer $t"
        $h['X-GitHub-Api-Version'] = '2022-11-28'
    }
    return $h
}

function New-TempDirectory {
    $p = Join-Path ([System.IO.Path]::GetTempPath()) ("dexel-install-" + [Guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $p -Force | Out-Null
    return $p
}

# --------------------------------------------------------------------------
# step 3 -- resolve the release
#
# PowerShell parses JSON properly, so unlike install.sh this side has no
# reason to avoid it -- ConvertFrom-Json is built in and always present.
# --------------------------------------------------------------------------

function Resolve-Release {
    if ($env:DEXEL_VERSION) {
        $url = "$($script:Api)/releases/tags/$($env:DEXEL_VERSION)"
        $what = "release $($env:DEXEL_VERSION)"
    } else {
        $url = "$($script:Api)/releases/latest"
        $what = 'the latest release'
    }

    Step "resolving $what of $($script:Repo)"
    try {
        $rel = Invoke-RestMethod -Uri $url -Headers (Get-Headers 'application/vnd.github+json') -UseBasicParsing
    } catch {
        Say ''
        Say "Could not read $url"
        if (Get-DexelToken) {
            Say "  A token was sent; check that it can read $($script:Repo)."
        } else {
            Say "  $($script:Repo) may still be private. Set a token that can read it:"
            Say '      $env:GH_TOKEN = (gh auth token)'
        }
        Say '  (An unauthenticated GitHub API allows 60 requests per hour per IP.)'
        Fail 7 "could not resolve $what : $($_.Exception.Message)"
    }

    # Set-StrictMode 2.0 turns a missing property into a terminating error,
    # so ASK whether tag_name is there before reading it -- an API answer we
    # do not understand should produce this message, not a stack trace.
    if (-not ($rel.PSObject.Properties.Name -contains 'tag_name') -or -not $rel.tag_name) {
        Fail 7 "no tag_name in the API response for $what."
    }
    if ($rel.tag_name -notmatch '^v[0-9]+\.[0-9]+\.[0-9]+') {
        Fail 7 "resolved tag `"$($rel.tag_name)`" is not a vX.Y.Z release tag."
    }

    Note "release   $($rel.tag_name)"
    Note "assets    $(@($rel.assets).Count)"
    return $rel
}

function Find-Asset($Release, [string]$Name) {
    foreach ($a in @($Release.assets)) {
        if ($a.name -eq $Name) { return $a }
    }
    return $null
}

# Two hosting paths, exactly as in install.sh: browser_download_url when
# anonymous (CDN-backed, no API rate limit -- the path every public install
# takes), the assets API URL with Accept: application/octet-stream when a
# token is in play, because github.com/.../releases/download/... answers 404
# to a bearer token on a private repo.
function Get-AssetRequest($Asset) {
    if (Get-DexelToken) {
        return @{ Uri = $Asset.url; Headers = (Get-Headers 'application/octet-stream') }
    }
    $u = $Asset.browser_download_url
    if (-not $u) { $u = $Asset.url }
    return @{ Uri = $u; Headers = (Get-Headers '*/*') }
}

function Save-Asset($Asset, [string]$OutFile) {
    $req = Get-AssetRequest $Asset
    try {
        Invoke-WebRequest -Uri $req.Uri -Headers $req.Headers -OutFile $OutFile -UseBasicParsing
    } catch {
        Fail 7 "download of $($Asset.name) failed: $($_.Exception.Message)"
    }
}

# --------------------------------------------------------------------------
# steps 4-6 -- the archive, its checksum, and the refusal to proceed
# --------------------------------------------------------------------------

function Get-ExpectedHash([string]$SumsFile, [string]$ArchiveName) {
    foreach ($line in (Get-Content -LiteralPath $SumsFile)) {
        $t = $line.Trim()
        if (-not $t) { continue }
        # "<hash>  <name>" or "<hash> *<name>" (binary-mode spelling).
        $parts = $t -split '\s+', 2
        if ($parts.Count -lt 2) { continue }
        $name = $parts[1].Trim()
        if ($name.StartsWith('*')) { $name = $name.Substring(1) }
        if ($name -eq $ArchiveName) { return $parts[0].ToLowerInvariant() }
    }
    return ''
}

function Assert-Checksum($Release, [string]$ArchivePath, [string]$ArchiveName) {
    $sumsAsset = Find-Asset $Release 'sha256sums.txt'
    if (-not $sumsAsset) {
        Fail 6 @"
release $($Release.tag_name) has no sha256sums.txt, so this download cannot be
verified. Refusing to install.
"@
    }
    Step 'downloading sha256sums.txt'
    $sums = Join-Path $script:TempDir 'sha256sums.txt'
    Save-Asset $sumsAsset $sums

    $want = Get-ExpectedHash $sums $ArchiveName
    if (-not $want) {
        Fail 6 @"
sha256sums.txt has no line for $ArchiveName, so this download cannot be
verified. Refusing to install.
"@
    }

    $got = (Get-FileHash -LiteralPath $ArchivePath -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($want -ne $got) {
        Say ''
        Say "  expected  $want   (from the release's sha256sums.txt)"
        Say "  actual    $got   ($ArchiveName)"
        Say ''
        Say '  Nothing was unpacked and nothing was installed.'
        Fail 6 "sha256 mismatch on $ArchiveName."
    }

    # Cross-check against the digest GitHub itself reports. Two different
    # systems produced these (build-release.sh hashed what it built; GitHub
    # hashed what it received) and they must never disagree.
    $asset = Find-Asset $Release $ArchiveName
    $apiDigest = ''
    if ($asset -and ($asset.PSObject.Properties.Name -contains 'digest') -and $asset.digest) {
        $apiDigest = ($asset.digest -replace '^sha256:', '').ToLowerInvariant()
    }
    if ($apiDigest -and $apiDigest -ne $want) {
        Say ''
        Say "  sha256sums.txt says  $want"
        Say "  the GitHub API says  $apiDigest"
        Fail 6 "the release's checksum file and GitHub disagree about $ArchiveName. Refusing to install."
    }

    Step 'sha256 verified'
    Note $got
}

# --------------------------------------------------------------------------
# step 8 (idempotency) -- a re-run is an upgrade in place
#
# `dexel status` exits 0 whether or not a runtime is running, so running-ness
# comes from `status --json`. Unlike Linux, Windows will not let us overwrite
# a running .exe, so here the stop is closer to a precondition than a
# courtesy -- but a stop that fails still must not fail the install, so the
# copy below reports the real reason if the file is locked.
# --------------------------------------------------------------------------

function Stop-RunningRuntime([string]$Exe) {
    if (-not (Test-Path -LiteralPath $Exe)) { return }
    $running = $false
    try {
        $json = & $Exe status --json 2>$null | Out-String
        if ($json) { $running = [bool](($json | ConvertFrom-Json).running) }
    } catch {
        $running = $false
    }
    if (-not $running) { return }

    Step 'a dexel runtime is running -- stopping it before upgrading'
    try {
        & $Exe stop 2>$null | Out-Null
        $script:StoppedRuntime = $true
        # Give Windows a moment to release the image before we overwrite it.
        Start-Sleep -Milliseconds 750
    } catch {
        Warn "could not stop the running runtime; trying the upgrade anyway."
    }
}

# --------------------------------------------------------------------------
# step 7 -- unpack and install
# --------------------------------------------------------------------------

function Install-Binary([string]$ArchivePath, [string]$BinDir, [string]$Tag, [string]$Arch) {
    $x = Join-Path $script:TempDir 'x'
    New-Item -ItemType Directory -Path $x -Force | Out-Null
    try {
        Expand-Archive -LiteralPath $ArchivePath -DestinationPath $x -Force
    } catch {
        Fail 4 "could not unpack the archive: $($_.Exception.Message)"
    }

    # build-release.sh stages each target as
    # dexel-<version>-windows-<arch>\{dexel.exe,README.md,LICENSE,...} and
    # zips that directory, so the binary is one level down. The recursive
    # search is the belt for a future flat archive's braces.
    $src = Join-Path (Join-Path $x "dexel-$Tag-windows-$Arch") 'dexel.exe'
    if (-not (Test-Path -LiteralPath $src)) {
        $found = Get-ChildItem -Path $x -Filter 'dexel.exe' -Recurse -File |
            Select-Object -First 1
        if (-not $found) { Fail 4 "no dexel.exe inside the archive." }
        $src = $found.FullName
    }

    New-Item -ItemType Directory -Path $BinDir -Force | Out-Null
    $dest = Join-Path $BinDir 'dexel.exe'
    try {
        Copy-Item -LiteralPath $src -Destination $dest -Force
    } catch {
        Fail 4 @"
could not write $dest : $($_.Exception.Message)
If a dexel runtime is still running, run ``dexel stop`` and re-run this installer.
"@
    }
    Step "installed $dest"
    return $dest
}

# Step 8 of the contract: the state dir and its logs dir, so a first `dexel`
# cannot fail on a missing directory (PLATFORM_NOTES.md section 1 -- %LOCALAPPDATA%\dexel).
function New-StateDirectories {
    $state = $env:DEXEL_HOME
    if (-not $state) {
        if (-not $env:LOCALAPPDATA) {
            Warn "%LOCALAPPDATA% is not set; dexel will resolve its own state directory on first run."
            return '(resolved by dexel on first run)'
        }
        $state = Join-Path $env:LOCALAPPDATA 'dexel'
    }
    try {
        New-Item -ItemType Directory -Path (Join-Path $state 'logs') -Force | Out-Null
    } catch {
        Warn "could not create $state\logs -- dexel will try again on first run."
    }
    return $state
}

# --------------------------------------------------------------------------
# step 9 -- the user PATH
#
# HKCU\Environment only, via SetEnvironmentVariable('...','User'), which also
# broadcasts WM_SETTINGCHANGE so new shells pick it up. Never the machine
# PATH (that needs elevation), never a duplicate entry.
# --------------------------------------------------------------------------

function Add-ToUserPath([string]$BinDir) {
    $current = [Environment]::GetEnvironmentVariable('Path', 'User')
    if (-not $current) { $current = '' }

    $normalized = $BinDir.TrimEnd('\')
    foreach ($entry in ($current -split ';')) {
        if ($entry.Trim().TrimEnd('\') -ieq $normalized) {
            Note "PATH      already includes $BinDir"
            return
        }
    }

    if ($env:DEXEL_NO_PATH -eq '1') {
        Say ''
        Say "$BinDir is not on your PATH (DEXEL_NO_PATH=1, so it was left alone):"
        Say "  [Environment]::SetEnvironmentVariable('Path', [Environment]::GetEnvironmentVariable('Path','User') + ';$BinDir', 'User')"
        return
    }

    $new = $current.TrimEnd(';')
    if ($new) { $new = "$new;$normalized" } else { $new = $normalized }
    try {
        [Environment]::SetEnvironmentVariable('Path', $new, 'User')
    } catch {
        Warn "could not update your user PATH: $($_.Exception.Message)"
        Say "Add it yourself, or run dexel by full path: $BinDir\dexel.exe"
        return
    }
    # The current session's copy was read at startup and does not see the
    # registry change, so patch it too -- otherwise `dexel` fails in the very
    # shell that just installed it.
    $env:Path = "$($env:Path);$normalized"
    Step "added $BinDir to your user PATH"
    Note 'Open a new terminal for other shells to see it.'
}

# --------------------------------------------------------------------------
# step 10 -- verify the installed binary, then report
# --------------------------------------------------------------------------

function Assert-Installed([string]$Exe, [string]$Tag) {
    $line = ''
    try { $line = (& $Exe version 2>$null | Out-String).Trim() } catch { $line = '' }
    if (-not $line) {
        Fail 8 @"
$Exe installed but ``dexel version`` produced nothing.
The archive may not match this machine's architecture.
"@
    }
    if ($line -notlike "*$Tag*") {
        Warn "``dexel version`` says `"$line`" but $Tag was installed."
    }
    return $line
}

function Write-Report([string]$VersionLine, [string]$Exe, [string]$StateDir) {
    Say ''
    Say $VersionLine
    Say "installed to $Exe"
    Say ''
    if ($script:StoppedRuntime) {
        Say 'The runtime that was running before this upgrade was stopped, and this'
        Say 'installer does not restart things for you. Start it again when you want it:'
        Say ''
    }
    Say 'Next:'
    Say '  dexel                    start the runtime and open the game'
    Say '  dexel status             is it running? what is it seeing?'
    Say '  dexel stop               shut it down (closing the tab does not)'
    Say '  dexel autostart enable   start dexel at login -- NOT enabled, this is opt-in'
    Say ''
    Say 'Autostart is off and nothing is running: this installer started no'
    Say 'processes and registered no login entries.'
    Say ''
    Say 'On Windows, read this before you play:'
    Say '  Activity tracking is NOT wired up yet. dexel has no native capture'
    Say '  provider for Windows, so it runs a deliberately BLIND, zero-signal'
    Say '  provider: the game, the UI and every command work, but your typing'
    Say '  does not accrue and the companion will not claim a workday it cannot'
    Say '  see. That is the honest failure mode, not a bug to report. Linux and'
    Say '  macOS have real providers.'
    Say ''
    Say 'Your keystrokes are counted, never read -- counts and durations only,'
    Say 'enforced by build-failing structural tests. Your data stays in'
    Say "$StateDir and upgrades never touch it."
    Say ''
}

# --------------------------------------------------------------------------
# main
# --------------------------------------------------------------------------

function Invoke-DexelInstall {
    $arch = Resolve-Platform
    Enable-Tls12

    $binDir = $env:DEXEL_INSTALL_DIR
    if (-not $binDir) {
        # Join-Path on a null root fails with "Cannot bind argument to
        # parameter 'Path'", which tells the user nothing. Say the real thing.
        if (-not $env:LOCALAPPDATA) {
            Fail 4 "%LOCALAPPDATA% is not set, so there is no default install location. Set `$env:DEXEL_INSTALL_DIR and re-run."
        }
        $binDir = Join-Path (Join-Path $env:LOCALAPPDATA 'dexel') 'bin'
    }

    $script:TempDir = New-TempDirectory
    Step "dexel installer -- windows-$arch"

    $rel = Resolve-Release                                          # 3
    $tag = $rel.tag_name
    $archiveName = "dexel-$tag-windows-$arch.zip"

    $asset = Find-Asset $rel $archiveName                           # 3b
    if (-not $asset -and -not $env:DEXEL_ARCHIVE) {
        Say ''
        Say "Release $tag contains:"
        foreach ($a in @($rel.assets)) { Say "    $($a.name)" }
        Fail 5 @"
no windows-$arch build in release $tag (expected $archiveName).
Build from source: https://github.com/$($script:Repo)#building-from-source
"@
    }

    $archivePath = Join-Path $script:TempDir $archiveName            # 4, 5
    if ($env:DEXEL_ARCHIVE) {
        if (-not (Test-Path -LiteralPath $env:DEXEL_ARCHIVE)) {
            Fail 2 "DEXEL_ARCHIVE=$($env:DEXEL_ARCHIVE) is not a file."
        }
        Step "using local archive $($env:DEXEL_ARCHIVE) (checksum still verified)"
        Copy-Item -LiteralPath $env:DEXEL_ARCHIVE -Destination $archivePath -Force
    } else {
        Step "downloading $archiveName"
        Save-Asset $asset $archivePath
    }

    Assert-Checksum $rel $archivePath $archiveName                   # 6

    if ($env:DEXEL_DRY_RUN -eq '1') {
        Say ''
        Say "DEXEL_DRY_RUN=1: resolved, downloaded and verified $archiveName."
        Say 'Nothing was installed.'
        return
    }

    Stop-RunningRuntime (Join-Path $binDir 'dexel.exe')              # 8
    $exe = Install-Binary $archivePath $binDir $tag $arch            # 7
    $state = New-StateDirectories                                    # 8
    $versionLine = Assert-Installed $exe $tag                        # 10
    Add-ToUserPath $binDir                                           # 9
    Write-Report $versionLine $exe $state                            # 10
}

try {
    Invoke-DexelInstall
}
catch {
    if ($script:ExitCode -eq 0) { $script:ExitCode = 1 }
    Say ''
    Write-Host "dexel install failed: $($_.Exception.Message)" -ForegroundColor Red
}
finally {
    if ($script:TempDir -and (Test-Path -LiteralPath $script:TempDir)) {
        Remove-Item -LiteralPath $script:TempDir -Recurse -Force -ErrorAction SilentlyContinue
    }
    $ProgressPreference = $script:OldProgress
}

# `exit` inside `irm ... | iex` would close the user's PowerShell session, so
# only a real script invocation exits with the code; a piped run leaves it in
# $LASTEXITCODE, which is what a CI step checks anyway.
$global:LASTEXITCODE = $script:ExitCode
if ($PSCommandPath) { exit $script:ExitCode }
