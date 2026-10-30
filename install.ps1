# install.ps1: install lazyssm on Windows.
#
#   irm https://raw.githubusercontent.com/chalvinwz/lazyssm/main/install.ps1 | iex
#
# The zip is verified against the release's checksums.txt before anything is
# installed. lazyssm.exe goes into a folder in your profile that is added to
# your user PATH, so no admin rights are needed. Works in Windows PowerShell
# 5.1 and PowerShell 7. There is no param() block and no exit: a failed
# install throws and leaves your PowerShell session open.
#
# Environment:
#   LAZYSSM_VERSION        release to install, e.g. v0.3.0 or 0.3.0 (default: latest)
#   LAZYSSM_INSTALL_DIR    where to put lazyssm.exe
#                          (default: $env:LOCALAPPDATA\Programs\lazyssm)
#   LAZYSSM_DOWNLOAD_BASE  https base URL of the release downloads, or a local
#                          directory laid out as <tag>\<file> (used by tests)
#                          (default: https://github.com/chalvinwz/lazyssm/releases/download)

# One script block keeps variables, functions and preferences out of the
# caller's session, and makes a truncated download fail to parse instead of
# running half a script.
& {
    $ErrorActionPreference = 'Stop'
    # Windows PowerShell 5.1 downloads far slower while drawing a progress bar.
    $ProgressPreference = 'SilentlyContinue'

    $RepoUrl = 'https://github.com/chalvinwz/lazyssm'
    $LatestReleaseUrl = "$RepoUrl/releases/latest"
    $LatestReleaseApi = 'https://api.github.com/repos/chalvinwz/lazyssm/releases/latest'
    $InstallShUrl = 'https://raw.githubusercontent.com/chalvinwz/lazyssm/main/install.sh'
    $AwsCliDocUrl = 'https://docs.aws.amazon.com/cli/latest/userguide/getting-started-install.html'
    $SsmPluginDocUrl = 'https://docs.aws.amazon.com/systems-manager/latest/userguide/session-manager-working-with-install-plugin.html'

    function Write-Step([string]$Message) {
        Write-Host $Message
    }

    function Write-Warn([string]$Message) {
        Write-Host "warning: $Message" -ForegroundColor Yellow
    }

    # Stop-Install fails with the message and one indented line per hint. It
    # throws instead of calling exit so the caller's session survives iex.
    function Stop-Install([string]$Message, [string[]]$Hint = @()) {
        $text = $Message
        foreach ($line in $Hint) {
            $text += [Environment]::NewLine + '  ' + $line
        }
        $exception = [Exception]::new($text)
        throw [Management.Automation.ErrorRecord]::new($exception, 'LazyssmInstallFailed',
            [Management.Automation.ErrorCategory]::InvalidResult, $null)
    }

    # Get-Arch returns this machine's arch as the release assets name it.
    # 32-bit PowerShell on 64-bit Windows sees x86 in PROCESSOR_ARCHITECTURE
    # and the real arch in PROCESSOR_ARCHITEW6432.
    function Get-Arch {
        $machine = $env:PROCESSOR_ARCHITEW6432
        if (-not $machine) {
            $machine = $env:PROCESSOR_ARCHITECTURE
        }
        switch ($machine) {
            'AMD64' { return 'amd64' }
            'ARM64' { return 'arm64' }
        }
        Stop-Install "unsupported architecture: $machine (lazyssm is built for amd64 and arm64)"
    }

    # Get-Source says where release files come from: a local directory (the
    # test seam), or an https URL.
    function Get-Source {
        $base = $env:LAZYSSM_DOWNLOAD_BASE
        if (-not $base) {
            $base = "$RepoUrl/releases/download"
        }
        if ($base.EndsWith('/') -or $base.EndsWith('\')) {
            $base = $base.Substring(0, $base.Length - 1)
        }
        if ($base -like 'https://*') {
            return @{ Local = $false; Base = $base }
        }
        $isDir = $false
        try {
            $isDir = Test-Path -LiteralPath $base -PathType Container
        } catch {
            $isDir = $false
        }
        if ($isDir) {
            return @{ Local = $true; Base = (Resolve-Path -LiteralPath $base).ProviderPath }
        }
        Stop-Install "LAZYSSM_DOWNLOAD_BASE must be an https:// URL or an existing directory, not '$base'"
    }

    # Get-LatestTag returns the tag that releases/latest redirects to, as
    # install.sh does, to avoid the API rate limit. If that fails it warns and
    # asks the GitHub API (60 unauthenticated requests an hour per IP).
    function Get-LatestTag {
        Write-Step 'Looking up the latest release'
        $redirectFailure = ''
        try {
            $resp = Invoke-WebRequest -UseBasicParsing -Method Head -Uri $LatestReleaseUrl
            # Read the final URI: 5.1 returns an HttpWebResponse, 7 an
            # HttpResponseMessage. Properties are looked up by name so a
            # caller's strict mode cannot fail on the missing one.
            $base = $resp.BaseResponse
            $final = ''
            if ($base.PSObject.Properties['ResponseUri']) {
                $final = [string]$base.ResponseUri.AbsoluteUri
            } elseif ($base.PSObject.Properties['RequestMessage']) {
                $final = [string]$base.RequestMessage.RequestUri.AbsoluteUri
            }
            if ($final -match '/releases/tag/([^/?#]+)') {
                return $Matches[1]
            }
            $redirectFailure = "it led to '$final', not to a release"
        } catch {
            $redirectFailure = $_.Exception.Message
        }
        Write-Warn "could not follow the releases/latest redirect ($redirectFailure); asking the GitHub API instead"
        $reasons = @("${LatestReleaseUrl}: $redirectFailure")
        try {
            $tag = [string](Invoke-RestMethod -UseBasicParsing -Uri $LatestReleaseApi).tag_name
            if ($tag) {
                return $tag
            }
            $reasons += "$LatestReleaseApi has no tag_name"
        } catch {
            $reasons += "${LatestReleaseApi}: $($_.Exception.Message)"
        }
        Stop-Install "could not look up the latest release at $LatestReleaseUrl" -Hint ($reasons + @('set LAZYSSM_VERSION (e.g. v0.3.0) to pick a release'))
    }

    # Get-Version returns the version to install, without its leading v: the
    # one LAZYSSM_VERSION names, or else the latest release.
    function Get-Version {
        $requested = $env:LAZYSSM_VERSION
        if (-not $requested) {
            $requested = Get-LatestTag
        }
        $version = $requested
        if ($version.StartsWith('v', [StringComparison]::Ordinal)) {
            $version = $version.Substring(1)
        }
        if ($version -cnotmatch '^[0-9][0-9A-Za-z.-]*\z') {
            Stop-Install "invalid version '$requested' (expected a release tag such as v0.3.0)"
        }
        return $version
    }

    # Get-InstallDir returns the absolute install dir without a trailing slash.
    function Get-InstallDir {
        $dir = $env:LAZYSSM_INSTALL_DIR
        if (-not $dir) {
            if (-not $env:LOCALAPPDATA) {
                Stop-Install 'LOCALAPPDATA is not set, so there is no default install dir' -Hint 'set LAZYSSM_INSTALL_DIR to the folder to install lazyssm into'
            }
            $dir = [IO.Path]::Combine($env:LOCALAPPDATA, 'Programs', 'lazyssm')
        }
        # Resolve relative paths from the PowerShell location, as cmdlets do.
        $dir = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($dir)
        $trimmed = $dir.TrimEnd('\', '/')
        if ($trimmed -and -not $trimmed.EndsWith(':')) {
            $dir = $trimmed
        }
        return $dir
    }

    # Save-ReleaseFile copies release file $Name of $Tag into $Dir and
    # returns its path there.
    function Save-ReleaseFile([hashtable]$Source, [string]$Tag, [string]$Name, [string]$Dir) {
        $path = [IO.Path]::Combine($Dir, $Name)
        if ($Source.Local) {
            try {
                [IO.File]::Copy([IO.Path]::Combine($Source.Base, $Tag, $Name), $path, $true)
            } catch {
                Stop-Install "could not get $Name for $Tag from $($Source.Base)"
            }
        } else {
            $url = "$($Source.Base)/$Tag/$Name"
            try {
                Invoke-WebRequest -UseBasicParsing -Uri $url -OutFile $path
            } catch {
                Stop-Install "could not download $url" -Hint $_.Exception.Message
            }
        }
        return $path
    }

    # Assert-Checksum stops the install unless the zip matches its exact-name
    # line in checksums.txt.
    function Assert-Checksum([string]$Sums, [string]$Zip, [string]$Archive, [string]$Tag) {
        $expected = ''
        foreach ($line in [IO.File]::ReadAllLines($Sums)) {
            $fields = $line.Trim() -split '\s+'
            if ($fields.Count -ge 2 -and $fields[1] -ceq $Archive) {
                $expected = $fields[0]
                break
            }
        }
        if (-not $expected) {
            Stop-Install "checksums.txt for $Tag has no entry for $Archive; not installing"
        }
        # On 5.1, script-module functions such as Get-FileHash and
        # Expand-Archive do not inherit $ErrorActionPreference, so they need
        # -ErrorAction Stop for a failure to reach the catch.
        try {
            $actual = (Get-FileHash -LiteralPath $Zip -Algorithm SHA256 -ErrorAction Stop).Hash.ToLowerInvariant()
        } catch {
            Stop-Install "could not compute the sha256 checksum of $Archive; not installing" -Hint $_.Exception.Message
        }
        if (-not [string]::Equals($actual, $expected, [StringComparison]::OrdinalIgnoreCase)) {
            Stop-Install "checksum mismatch for $Archive; not installing" -Hint "expected sha256 $expected", "got sha256      $actual"
        }
        Write-Step 'Verified sha256 checksum'
    }

    # Expand-Release unpacks the zip into $Dir and returns the path of the
    # lazyssm.exe at its root.
    function Expand-Release([string]$Zip, [string]$Archive, [string]$Dir) {
        $out = [IO.Path]::Combine($Dir, 'extract')
        try {
            Expand-Archive -LiteralPath $Zip -DestinationPath $out -Force -ErrorAction Stop
        } catch {
            Stop-Install "could not extract $Archive" -Hint $_.Exception.Message
        }
        $exe = [IO.Path]::Combine($out, 'lazyssm.exe')
        if (-not [IO.File]::Exists($exe)) {
            Stop-Install "$Archive has no lazyssm.exe at its root"
        }
        return $exe
    }

    # Install-Binary copies lazyssm.exe, and only it, into $Dir, replacing an
    # older one, and returns its path there.
    function Install-Binary([string]$Exe, [string]$Dir) {
        $target = [IO.Path]::Combine($Dir, 'lazyssm.exe')
        try {
            [void][IO.Directory]::CreateDirectory($Dir)
            [IO.File]::Copy($Exe, $target, $true)
        } catch {
            Stop-Install "could not install $target" -Hint $_.Exception.Message, 'if lazyssm is running, close it and run the installer again'
        }
        return $target
    }

    # Remove-TempDir deletes the temp dir, retrying briefly: a virus scanner
    # may still hold a file that was just extracted.
    function Remove-TempDir([string]$Path) {
        for ($i = 0; $i -lt 10; $i++) {
            try {
                if ([IO.Directory]::Exists($Path)) {
                    [IO.Directory]::Delete($Path, $true)
                }
                return
            } catch {
                Start-Sleep -Milliseconds 200
            }
        }
        Write-Warn "could not remove the temp dir $Path"
    }

    # Get-InstalledVersion returns what `lazyssm --version` prints, or $null
    # if it does not run.
    function Get-InstalledVersion([string]$Target) {
        # Output on the binary's stderr must not become a terminating error.
        $ErrorActionPreference = 'Continue'
        try {
            $out = @(& $Target --version 2>$null)
            if ($LASTEXITCODE -eq 0 -and $out.Count -gt 0) {
                return ([string]$out[0]).Trim()
            }
        } catch {
            # The binary did not start; the caller warns.
        }
        return $null
    }

    # Test-PathList reports whether the semicolon-separated $List names $Dir,
    # ignoring case, quotes and a trailing backslash, and expanding %VAR%
    # references, as Windows does.
    function Test-PathList([string]$List, [string]$Dir) {
        $want = $Dir.TrimEnd('\', '/')
        foreach ($entry in ($List -split ';')) {
            $entry = $entry.Trim().Trim('"')
            if (-not $entry) {
                continue
            }
            foreach ($candidate in @($entry, [Environment]::ExpandEnvironmentVariables($entry))) {
                if ([string]::Equals($candidate.TrimEnd('\', '/'), $want, [StringComparison]::OrdinalIgnoreCase)) {
                    return $true
                }
            }
        }
        return $false
    }

    # Add-UserPath appends $Dir to the user Path unless it is already there,
    # and reports whether it did. The value is read and written raw, so
    # entries such as %USERPROFILE%\bin stay unexpanded and the registry type
    # (normally REG_EXPAND_SZ) is kept.
    function Add-UserPath([string]$Dir) {
        $key = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey('Environment')
        try {
            $raw = ''
            $kind = [Microsoft.Win32.RegistryValueKind]::ExpandString
            if (@($key.GetValueNames()) -contains 'Path') {
                $raw = [string]$key.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
                $kind = $key.GetValueKind('Path')
            }
            if (Test-PathList $raw $Dir) {
                return $false
            }
            if ($kind -ne [Microsoft.Win32.RegistryValueKind]::String -and $kind -ne [Microsoft.Win32.RegistryValueKind]::ExpandString) {
                throw "the user Path is stored as $kind, not as text"
            }
            if ($raw -and -not $raw.EndsWith(';')) {
                $raw += ';'
            }
            $key.SetValue('Path', $raw + $Dir, $kind)
        } finally {
            $key.Close()
        }
        # Setting a user variable through .NET broadcasts WM_SETTINGCHANGE, so
        # Explorer and terminals started from now on see the new Path without
        # a sign-out. Removing a nonexistent variable changes nothing else.
        [Environment]::SetEnvironmentVariable('LAZYSSM_PATH_REFRESH', $null, 'User')
        return $true
    }

    # Update-Path puts $Dir on the user PATH and on this session's, then
    # warns if another lazyssm would still run first.
    function Update-Path([string]$Dir, [string]$Target) {
        try {
            if (Add-UserPath $Dir) {
                Write-Step "Added $Dir to your user PATH; open a new terminal to use lazyssm"
            }
        } catch {
            Write-Warn "could not add $Dir to your user PATH ($($_.Exception.Message)); add it yourself, or run $Target directly"
        }
        if (-not (Test-PathList $env:Path $Dir)) {
            if ($env:Path -and -not $env:Path.EndsWith(';')) {
                $env:Path += ';'
            }
            $env:Path += $Dir
        }
        $first = Get-Command lazyssm -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
        if ($first -and -not [string]::Equals($first.Path, $Target, [StringComparison]::OrdinalIgnoreCase)) {
            Write-Warn "$($first.Path) comes first on your PATH and will run instead of $Target; remove it or reorder PATH"
        }
    }

    function Write-AwsToolWarning {
        if (-not (Get-Command aws -CommandType Application -ErrorAction SilentlyContinue)) {
            Write-Warn "aws (AWS CLI v2) not found; lazyssm needs it: $AwsCliDocUrl"
        }
        if (-not (Get-Command session-manager-plugin -CommandType Application -ErrorAction SilentlyContinue)) {
            Write-Warn "session-manager-plugin not found; lazyssm needs it: $SsmPluginDocUrl"
        }
    }

    if ($PSVersionTable.PSEdition -eq 'Core' -and -not $IsWindows) {
        Stop-Install "install.ps1 is for Windows; this is $($PSVersionTable.OS)" -Hint 'in a terminal, use install.sh instead:', "  curl -fsSL $InstallShUrl | sh", "or install with Homebrew; see $RepoUrl"
    }
    if ($PSVersionTable.PSEdition -ne 'Core') {
        # Windows PowerShell 5.1 may offer only TLS 1.0, which GitHub refuses,
        # so enable TLS 1.2 for this session.
        $tls12 = [Net.SecurityProtocolType]::Tls12
        if (([Net.ServicePointManager]::SecurityProtocol -band $tls12) -ne $tls12) {
            [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor $tls12
        }
    }

    $arch = Get-Arch
    $source = Get-Source
    $version = Get-Version
    $tag = "v$version"
    $dir = Get-InstallDir
    $archive = "lazyssm_${version}_windows_${arch}.zip"
    Write-Step "Installing lazyssm $tag (windows/$arch) into $dir"

    $tmp = [IO.Path]::Combine([IO.Path]::GetTempPath(), 'lazyssm-' + [Guid]::NewGuid().ToString('N'))
    try {
        [void][IO.Directory]::CreateDirectory($tmp)
        Write-Step "Downloading $archive"
        $sums = Save-ReleaseFile $source $tag 'checksums.txt' $tmp
        $zip = Save-ReleaseFile $source $tag $archive $tmp
        Assert-Checksum $sums $zip $archive $tag
        $exe = Expand-Release $zip $archive $tmp
        $target = Install-Binary $exe $dir
    } finally {
        Remove-TempDir $tmp
    }

    Write-Step "Installed $target"
    $installed = Get-InstalledVersion $target
    if ($installed) {
        Write-Step $installed
    } else {
        Write-Warn "'$target --version' failed; the binary may not run on this system"
    }
    Update-Path $dir $target
    Write-AwsToolWarning
}
