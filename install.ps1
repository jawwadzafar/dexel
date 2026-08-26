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
# Never elevates. Writes only under %LOCALAPPDATA%, %APPDATA% and HKCU. Never
# enables autostart.
#
# It DOES do desktop integration and it DOES start dexel, because "installed"
# and "running" are not the same thing and a one-command install that leaves
# you at a prompt has not finished the job:
#   * dexel.ico is copied out of the archive next to dexel.exe
#   * a Start Menu shortcut ("Dexel") is created in the PER-USER Programs
#     folder via WScript.Shell, targeting `dexel.exe open` with that icon --
#     so dexel is in the Start Menu and searchable, not only on PATH
#   * `dexel open` runs at the end, which starts the runtime and shows the UI
#
# Starting a process you just asked for is not the consent question;
# *enabling autostart* is, and that stays off and stays a separate explicit
# `dexel autostart enable`. There is no Windows equivalent of the Linux
# AppImage shell yet, so `dexel open` here opens your browser -- a supported
# front door, not a consolation prize.
#
# Environment (a `| iex` one-liner cannot pass parameters, so everything is
# an environment variable):
#   $env:DEXEL_INSTALL_DIR   where dexel.exe goes (default %LOCALAPPDATA%\dexel\bin)
#   $env:DEXEL_NO_START      "1" = install everything, start nothing
#   $env:DEXEL_NO_DESKTOP    "1" = no icon and no Start Menu shortcut
#   $env:DEXEL_VERSION       install this tag instead of the latest release
#   $env:DEXEL_REPO          resolve against a different repository
#   $env:DEXEL_ARCHIVE       use a .zip already on disk (checksum still verified)
#   $env:DEXEL_DRY_RUN       "1" = resolve + download + verify, then stop
#   $env:DEXEL_NO_PATH       "1" = do not touch the user PATH
#   $env:GH_TOKEN / $env:GITHUB_TOKEN
#                            bearer token; needed only while the repo is private
#   $env:DEXEL_NO_COLOR / $env:NO_COLOR
#                            "1" = plain text, no colour, no spinner (also
#                            automatic when output is redirected/piped)
#   $env:DEXEL_QUIET         "1" = suppress the banner and the spinner
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
$script:UnpackedDir = ''
$script:Launched = 'none'

# ---------------------------------------------------------------------------
# presentation -- colour, the banner, and the spinner
#
# Windows PowerShell renders colour through Write-Host -ForegroundColor (the
# console API), NOT ANSI escape sequences, so a redirected/piped run is plain
# text automatically -- there are no escape codes to leak. The one gate below
# still decides whether to COLOUR and whether to ANIMATE, keying off output
# redirection (the pipe case) plus NO_COLOR / DEXEL_NO_COLOR and DEXEL_QUIET.
# ---------------------------------------------------------------------------

$script:UseColor = $false
$script:Quiet    = $false
$script:Spinner  = $null

function Initialize-Presentation {
    if ($env:DEXEL_QUIET -eq '1') { $script:Quiet = $true }
    if ($env:DEXEL_NO_COLOR -eq '1' -or $env:NO_COLOR) { $script:UseColor = $false; return }
    try {
        if ([Console]::IsOutputRedirected) { $script:UseColor = $false; return }
    } catch {
        $script:UseColor = $false; return
    }
    $script:UseColor = $true
}

# Write-Marked -- a coloured prefix ("==>", "OK", "!") + a plain message, or
# the same line uncoloured when colour is off. One code path, two looks.
function Write-Marked([string]$Prefix, [string]$Color, [string]$Text) {
    if ($script:UseColor) {
        Write-Host $Prefix -ForegroundColor $Color -NoNewline
        Write-Host " $Text"
    } else {
        Write-Host "$Prefix $Text"
    }
}

function Say([string]$Text)  { Write-Host $Text }
function Step([string]$Text) { Write-Marked '==>' 'Magenta' $Text }
function Ok([string]$Text)   { Write-Marked 'OK' 'Green' $Text }
function Note([string]$Text) {
    if ($script:UseColor) { Write-Host "  $Text" -ForegroundColor DarkGray }
    else { Write-Host "  $Text" }
}
function Warn([string]$Text) { Write-Warning $Text }

# Show-Banner -- the DEXEL wordmark, once per run. Byte-for-byte the same ASCII
# art install.sh draws (PowerShell 5.1 is ASCII-only). $Version and $Target are
# the best hints known before the release is resolved.
function Show-Banner([string]$Version, [string]$Target) {
    if ($script:Quiet) { return }
    $fig = @(
        '       _                        _',
        '    __| |   ___  __  __   ___  | |',
        '   / _` |  / _ \ \ \/ /  / _ \ | |',
        '  | (_| | |  __/  >  <  |  __/ | |',
        '   \__,_|  \___| /_/\_\  \___| |_|'
    )
    Write-Host ''
    foreach ($line in $fig) {
        if ($script:UseColor) { Write-Host $line -ForegroundColor Magenta }
        else { Write-Host $line }
    }
    Write-Host ''
    if ($script:UseColor) {
        Write-Host '  a cozy pixel-art companion that runs on your real typing' -ForegroundColor DarkGray
    } else {
        Write-Host '  a cozy pixel-art companion that runs on your real typing'
    }
    Write-Host "  installing dexel $Version on $Target"
    Write-Host ''
}

# Start-Spinner / Stop-Spinner -- an animated frame + label while a blocking
# operation (the release download) runs. The animation lives in its own
# runspace so the main thread is free to do the real work; Stop-Spinner is
# idempotent and is ALSO invoked from the outer finally, so no failure path can
# leave a runspace running or a half-drawn line on screen. When colour/animation
# is off it degrades to a plain "==> label" step and a matching "OK" line.
function Start-Spinner([string]$Label) {
    if (-not $script:UseColor -or $script:Quiet) { Step $Label; $script:Spinner = $null; return }
    $flag = [hashtable]::Synchronized(@{ Stop = $false })
    $rs = [runspacefactory]::CreateRunspace()
    $rs.Open()
    $ps = [powershell]::Create()
    $ps.Runspace = $rs
    $null = $ps.AddScript({
        param($flag, $label)
        $frames = @('-', '\', '|', '/')
        $i = 0
        while (-not $flag.Stop) {
            $f = $frames[$i % 4]
            try {
                [Console]::Write("`r")
                [Console]::Write("$f $label")
            } catch { $null = $_ }
            Start-Sleep -Milliseconds 100
            $i++
        }
    }).AddArgument($flag).AddArgument($Label)
    $async = $ps.BeginInvoke()
    $script:Spinner = @{ Flag = $flag; PS = $ps; RS = $rs; Async = $async; Width = ($Label.Length + 4) }
}

function Stop-Spinner {
    if (-not $script:Spinner) { return }
    $s = $script:Spinner
    $script:Spinner = $null
    try { $s.Flag.Stop = $true } catch { $null = $_ }
    try { if ($s.Async) { $s.PS.EndInvoke($s.Async) } } catch { $null = $_ }
    try { $s.PS.Dispose() } catch { $null = $_ }
    try { $s.RS.Close(); $s.RS.Dispose() } catch { $null = $_ }
    # Erase the animated line: carriage return, blanks, carriage return.
    try { [Console]::Write("`r" + (' ' * $s.Width) + "`r") } catch { $null = $_ }
}

# Complete-Spinner MESSAGE -- stop the animation, then confirm with an OK line.
function Complete-Spinner([string]$Text) { Stop-Spinner; Ok $Text }

# Fail is the only failure path: it records the exit code the caller should
# see, then throws so the single try/catch at the bottom does the reporting
# and the cleanup in one place.
function Fail([int]$Code, [string]$Message) {
    $script:ExitCode = $Code
    Stop-Spinner
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

    Ok 'sha256 verified'
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

    # dexel.ico rides in the same directory as dexel.exe
    # (scripts/build-release.sh stages it there), so remember where that was
    # rather than unpacking the archive a second time to find it.
    $script:UnpackedDir = Split-Path -Parent $src

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
    Ok "installed $dest"
    return $dest
}

# --------------------------------------------------------------------------
# step 7b/7c -- desktop integration
#
# Two cheap things, both per-user and both plain files, so neither needs
# elevation and neither can fail in a way that matters:
#
#   dexel.ico   copied out of the archive next to dexel.exe. A .lnk stores a
#               PATH to its icon, not the pixels, so the file has to live
#               somewhere permanent -- next to the exe it belongs to.
#   Dexel.lnk   the Start Menu entry, in the PER-USER Programs folder.
#
# Both run AFTER the binary is in place and both are best-effort: a Start
# Menu that will not take a shortcut is not a reason to fail an install of a
# CLI that works fine from a terminal.
#
# There is no Windows analogue of install.sh's AppImage step. The Tauri
# shell publishes no Windows bundle on the release yet, so the shortcut
# targets `dexel.exe open`, which opens the browser -- and will silently
# start opening a real window the day a `dexel-desktop.exe` appears beside
# it, because that lookup lives in the binary (ARCHITECTURE.md Decision 17),
# not here.
# --------------------------------------------------------------------------

function Install-ShortcutIcon([string]$BinDir) {
    if (-not $script:UnpackedDir) { return '' }
    $src = Join-Path $script:UnpackedDir 'dexel.ico'
    if (-not (Test-Path -LiteralPath $src)) {
        Note "icon      not in this archive (a release from before the icon shipped)"
        return ''
    }
    $dest = Join-Path $BinDir 'dexel.ico'
    try {
        Copy-Item -LiteralPath $src -Destination $dest -Force
    } catch {
        Warn "could not write $dest ; the shortcut will use the default icon."
        return ''
    }
    return $dest
}

# Get-ProgramsFolder -- the per-user Start Menu Programs directory.
#
# GetFolderPath('Programs') is the documented way and needs no elevation
# (the ALL-USERS equivalent, CommonPrograms, would). The %APPDATA% fallback
# exists because GetFolderPath returns an empty string rather than throwing
# on a profile where the shell folder is not registered.
function Get-ProgramsFolder {
    $programs = ''
    try { $programs = [Environment]::GetFolderPath('Programs') } catch { $programs = '' }
    if ($programs) { return $programs }
    if (-not $env:APPDATA) { return '' }
    $p = Join-Path $env:APPDATA 'Microsoft'
    $p = Join-Path $p 'Windows'
    $p = Join-Path $p 'Start Menu'
    return (Join-Path $p 'Programs')
}

function Install-StartMenuShortcut([string]$Exe, [string]$IconPath) {
    $programs = Get-ProgramsFolder
    if (-not $programs) {
        Warn "could not locate your Start Menu folder; skipping the shortcut."
        return ''
    }
    try {
        New-Item -ItemType Directory -Path $programs -Force | Out-Null
    } catch {
        Warn "could not create $programs ; skipping the shortcut."
        return ''
    }

    $lnk = Join-Path $programs 'Dexel.lnk'
    # WScript.Shell rather than a .NET shortcut library: it is the COM object
    # every supported Windows already has, it works identically on PowerShell
    # 5.1 and 7, and it needs no assembly to load. Overwriting an existing
    # .lnk is what makes a re-run idempotent.
    try {
        $wsh = New-Object -ComObject WScript.Shell
        $sc = $wsh.CreateShortcut($lnk)
        $sc.TargetPath = $Exe
        $sc.Arguments = 'open'
        $sc.WorkingDirectory = (Split-Path -Parent $Exe)
        $sc.Description = 'Dexel -- a cozy pixel-art companion that works the day alongside you'
        if ($IconPath) { $sc.IconLocation = "$IconPath,0" }
        $sc.Save()
    } catch {
        Warn "could not create the Start Menu shortcut: $($_.Exception.Message)"
        return ''
    }
    # No explicit ReleaseComObject: WScript.Shell is in-process, the RCW is
    # collected with the runspace, and there is no external process to leave
    # running. An installer is not a long-lived host.

    Ok "installed $lnk"
    if ($IconPath) {
        Note 'dexel is now in your Start Menu, with its own icon'
    } else {
        Note 'dexel is now in your Start Menu'
    }
    return $lnk
}

function Install-DesktopIntegration([string]$Exe, [string]$BinDir) {
    if ($env:DEXEL_NO_DESKTOP -eq '1') {
        Note 'desktop   skipped ($env:DEXEL_NO_DESKTOP = 1)'
        return ''
    }
    $icon = Install-ShortcutIcon $BinDir
    return (Install-StartMenuShortcut $Exe $icon)
}

# --------------------------------------------------------------------------
# step 11 -- just start
#
# The one thing this installer's older self refused to do, and the one thing
# it was wrong about. `dexel open` starts the runtime if it is not already up
# and then shows the UI. Enabling autostart -- making dexel come back on
# every login forever -- is the consent question, and it is untouched.
#
# A failure here does not fail the install. Out-Host keeps dexel's own
# output on the console instead of letting it become this function's return
# value, and the exit code is read from $LASTEXITCODE because a native
# command that exits non-zero does not throw.
# --------------------------------------------------------------------------

function Start-Dexel([string]$Exe) {
    $script:Launched = 'none'
    if ($env:DEXEL_NO_START -eq '1') { return }
    Say ''
    Step 'starting dexel and opening the game'
    try {
        & $Exe open | Out-Host
    } catch {
        Warn "starting dexel did not finish cleanly: $($_.Exception.Message)"
        Say "Check it yourself: $Exe status"
        return
    }
    if ($LASTEXITCODE -eq 0) {
        $script:Launched = 'open'
        return
    }
    Warn "dexel is installed, but ``dexel open`` exited $LASTEXITCODE."
    Say "Check it yourself: $Exe status"
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

function Write-Report([string]$VersionLine, [string]$Exe, [string]$StateDir, [string]$Shortcut) {
    Say ''
    if ($script:UseColor) {
        Write-Host 'OK' -ForegroundColor Green -NoNewline
        Write-Host " dexel is installed  " -NoNewline
        Write-Host $VersionLine -ForegroundColor White
    } else {
        Say "OK dexel is installed  $VersionLine"
    }
    Say "installed to $Exe"
    if ($Shortcut) { Say "in your Start Menu as `"Dexel`"" }
    Say ''

    if ($script:Launched -eq 'open') {
        Say 'dexel is RUNNING and the game is open. That is the install finished,'
        Say 'not a side effect.'
    } elseif ($env:DEXEL_NO_START -eq '1') {
        Say 'Nothing was started ($env:DEXEL_NO_START = 1). Run `dexel` when you want it.'
    } else {
        Say 'Nothing is running. Run `dexel` to start it.'
    }
    if ($script:StoppedRuntime -and $script:Launched -eq 'none') {
        Say ''
        Say 'The runtime that was running before this upgrade was stopped and not'
        Say 'restarted. Run `dexel` to bring the new build up.'
    }
    Say ''
    Say 'Commands:'
    Say '  dexel                    start the runtime and open the game'
    Say '  dexel status             is it running? what is it seeing?'
    Say '  dexel stop               shut it down (closing the window does not)'
    Say '  dexel autostart enable   start dexel at login -- NOT enabled, this is opt-in'
    Say ''
    if ($script:Launched -eq 'none') {
        Say 'Autostart is OFF and nothing is running: this installer enabled no'
        Say 'services and registered no login items. Making dexel come back on every'
        Say 'login is a separate, explicit `dexel autostart enable`.'
    } else {
        Say 'Autostart is OFF. This installer started dexel because you just asked for'
        Say 'dexel; it enabled no services and registered no login items. Making it come'
        Say 'back on every login is a separate, explicit `dexel autostart enable`.'
    }
    Say ''
    Say 'On Windows, read this before you play:'
    Say '  Activity tracking IS wired up, and it is NEW IN THIS BUILD -- field'
    Say '  verification is pending. dexel installs two low-level Windows hooks'
    Say '  that COUNT your keystrokes and mouse activity globally (no permission'
    Say '  prompt, and no key is ever identified, stored or logged). What has not'
    Say '  happened yet is anybody running it on real Windows hardware: this'
    Say '  project has no Windows CI runner, so the parts that can be tested from'
    Say '  Linux are tested and the hook install itself is not.'
    Say ''
    Say '  If Windows refuses the hooks -- enterprise policy, a locked desktop --'
    Say '  dexel reports itself BLIND rather than pretending to see, and the'
    Say '  companion will not claim a workday it cannot see. That is the honest'
    Say '  failure mode, not a bug to report. Run `dexel status` to see which'
    Say '  state you are in.'
    Say ''
    Say '  If typing does not accrue on a machine that says it is not blind, that'
    Say '  IS worth reporting -- with the tail of your runtime log.'
    Say ''
    Say 'Uninstall -- one command, and it is the exact reversal of the above:'
    Say ''
    Say '  dexel uninstall           stop, disable autostart, remove every file'
    Say '                            this installer wrote. Your save STAYS.'
    Say '  dexel uninstall --purge   ...and delete the save too (asks twice)'
    Say ''
    Say 'It prints every path it removed and every path it kept, and running it'
    Say 'twice is harmless. Add --yes to skip the prompts in a script.'
    Say ''
    Say 'One Windows-specific detail, stated because you will see it: a running'
    Say '.exe cannot delete itself, so uninstall schedules a detached helper that'
    Say 'removes dexel.exe the moment the command exits, and appends one line to'
    Say 'the runtime log saying whether it worked.'
    Say ''
    Say 'By hand instead -- or if you deleted the binary before reading this:'
    Say '  dexel stop; dexel autostart disable'
    Say "  Remove-Item `"$Exe`""
    # The .ico lives next to the exe (Install-ShortcutIcon): a .lnk stores a
    # PATH to its icon, not the pixels. Built into a variable first rather
    # than as a subexpression inside the escaped-quote string below, which is
    # correct PowerShell but nobody on this project can run a parser to prove
    # it (see this file's header on the authored-not-executed split).
    $icoPath = Join-Path (Split-Path -Parent $Exe) 'dexel.ico'
    Say "  Remove-Item `"$icoPath`""
    if ($Shortcut) { Say "  Remove-Item `"$Shortcut`"" }
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
    Initialize-Presentation
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
    $verHint = $env:DEXEL_VERSION
    if (-not $verHint) { $verHint = 'latest' }
    Show-Banner $verHint "windows-$arch"

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
        Start-Spinner "Downloading dexel $tag..."
        Save-Asset $asset $archivePath
        Complete-Spinner "downloaded $archiveName"
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
    $lnk = Install-DesktopIntegration $exe $binDir                   # 7b, 7c
    $state = New-StateDirectories                                    # 8
    $versionLine = Assert-Installed $exe $tag                        # 10
    Add-ToUserPath $binDir                                           # 9
    Start-Dexel $exe                                                 # 11
    Write-Report $versionLine $exe $state $lnk                       # 10
}

try {
    Invoke-DexelInstall
}
catch {
    if ($script:ExitCode -eq 0) { $script:ExitCode = 1 }
    Stop-Spinner
    Say ''
    if ($script:UseColor) {
        Write-Host 'x' -ForegroundColor Red -NoNewline
        Write-Host " dexel install failed: $($_.Exception.Message)"
    } else {
        Write-Host "x dexel install failed: $($_.Exception.Message)"
    }
}
finally {
    Stop-Spinner
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
