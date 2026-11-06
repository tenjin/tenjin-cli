# Installs the Tenjin CLI (the tenjin command) on Windows from its GitHub releases.
#
#   irm https://raw.githubusercontent.com/tenjin/tenjin-cli/main/install.ps1 | iex
#
# TENJIN_VERSION       a version to install, such as 1.0.0 (default: the latest)
# TENJIN_INSTALL_DIR   where to put tenjin.exe (default:
#                      %LOCALAPPDATA%\Programs\tenjin)
#
# The archive is checked against the release's checksums.txt before anything
# is installed. The folder is added to the user's PATH; nothing needs
# administrator rights. It is the Windows counterpart of install.sh: change
# both together.

& {
	$ErrorActionPreference = 'Stop'
	# Windows PowerShell draws progress so slowly that it holds up downloads.
	$ProgressPreference = 'SilentlyContinue'
	[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

	$releases = if ($env:TENJIN_RELEASES_URL) { $env:TENJIN_RELEASES_URL } else { 'https://github.com/tenjin/tenjin-cli/releases' }

	# Failing throws rather than exits: this runs inside the caller's shell.
	function Fail($message) { throw "Tenjin CLI installer: $message" }
	# How a folder is compared with PATH's entries: C:\tools\ and C:\tools
	# are the same folder.
	function Key($folder) { $folder.TrimEnd('\', '/') }

	$arch = $null
	try { $arch = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString() } catch { }
	if (-not $arch) {
		$arch = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
	}
	switch -Regex ($arch) {
		'^(X64|AMD64)$' { $arch = 'amd64' }
		'^ARM64$' { $arch = 'arm64' }
		default { Fail "no build for the $arch processor" }
	}

	$version = $env:TENJIN_VERSION
	if (-not $version) {
		# The latest release redirects to its tag's page.
		try {
			$response = Invoke-WebRequest -Uri "$releases/latest" -UseBasicParsing -Method Head
		} catch {
			Fail "could not find the latest release at $releases"
		}
		$final = if ($response.BaseResponse.ResponseUri) { $response.BaseResponse.ResponseUri } else { $response.BaseResponse.RequestMessage.RequestUri }
		# With no release yet, it stays on the releases page.
		if ($final.AbsoluteUri -notmatch '/tag/') { Fail "no release of the Tenjin CLI has been published at $releases yet" }
		$version = $final.AbsoluteUri.TrimEnd('/').Split('/')[-1]
	}
	$version = $version -replace '^v', ''
	if (-not $version -or $version -eq 'latest') { Fail 'could not tell which version is the latest' }

	$archive = "tenjin_${version}_windows_${arch}.zip"
	$url = "$releases/download/v$version"

	$tmp = Join-Path ([System.IO.Path]::GetTempPath()) ("tenjin-" + [System.Guid]::NewGuid().ToString('N'))
	New-Item -ItemType Directory -Path $tmp | Out-Null
	try {
		Write-Host "Downloading the Tenjin CLI $version for windows/$arch"
		try {
			Invoke-WebRequest -Uri "$url/$archive" -OutFile (Join-Path $tmp $archive) -UseBasicParsing
		} catch {
			Fail "could not download $url/$archive"
		}
		try {
			Invoke-WebRequest -Uri "$url/checksums.txt" -OutFile (Join-Path $tmp 'checksums.txt') -UseBasicParsing
		} catch {
			Fail "could not download $url/checksums.txt"
		}

		$want = $null
		foreach ($line in Get-Content (Join-Path $tmp 'checksums.txt')) {
			$fields = $line -split '\s+'
			if ($fields.Count -ge 2 -and $fields[1] -eq $archive) { $want = $fields[0].ToLower() }
		}
		if (-not $want) { Fail "$archive is not listed in checksums.txt" }
		$got = (Get-FileHash -Algorithm SHA256 -Path (Join-Path $tmp $archive)).Hash.ToLower()
		if ($got -ne $want) { Fail 'the download does not match its checksum; nothing was installed' }

		Expand-Archive -Path (Join-Path $tmp $archive) -DestinationPath (Join-Path $tmp 'out') -Force
		$binary = Join-Path $tmp 'out\tenjin.exe'
		if (-not (Test-Path $binary)) { Fail 'the archive has no tenjin.exe' }

		# The folder: the one asked for; or where tenjin already is, when that
		# is the user's own, so an update replaces it and no older copy is
		# left ahead of it on PATH; or the usual one.
		$dir = $env:TENJIN_INSTALL_DIR
		if (-not $dir) {
			$existing = Get-Command tenjin -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
			if ($existing -and $existing.Source.StartsWith($env:LOCALAPPDATA, [StringComparison]::OrdinalIgnoreCase)) {
				$dir = Split-Path -Parent $existing.Source
			}
		}
		if (-not $dir) { $dir = Join-Path $env:LOCALAPPDATA 'Programs\tenjin' }
		# A relative folder would put a relative entry on PATH.
		$dir = [System.IO.Path]::GetFullPath($dir)
		# Without the trailing slash tab completion leaves, unless it is a
		# drive's root, which needs it.
		if ($dir -ne [System.IO.Path]::GetPathRoot($dir)) { $dir = $dir.TrimEnd('\', '/') }
		New-Item -ItemType Directory -Path $dir -Force | Out-Null
		$target = Join-Path $dir 'tenjin.exe'
		# Windows cannot overwrite a running program but can rename it, so the
		# old one is moved aside first, under a name of its own in case an
		# earlier one is still held by a program that is running.
		$aside = $null
		if (Test-Path $target) {
			$aside = "$target.old-" + [System.Guid]::NewGuid().ToString('N').Substring(0, 8)
			Move-Item -Path $target -Destination $aside
		}
		try {
			Copy-Item -Path $binary -Destination $target
		} catch {
			# Put the old one back: a failed update must not leave no tenjin.
			if ($aside) { Move-Item -Path $aside -Destination $target -Force -ErrorAction SilentlyContinue }
			Fail "could not write $target; the version installed before is unchanged"
		}
		Get-ChildItem -Path $dir -Filter 'tenjin.exe.old-*' -ErrorAction SilentlyContinue | Remove-Item -Force -ErrorAction SilentlyContinue

		# The user's PATH is read and written unexpanded, so entries such as
		# %USERPROFILE%\bin stay as they are.
		$key = Get-Item -Path 'HKCU:\Environment'
		$userPath = $key.GetValue('Path', '', 'DoNotExpandEnvironmentNames')
		$entries = @($userPath -split ';' | Where-Object { $_ })
		if (@($entries | ForEach-Object { Key $_ }) -notcontains (Key $dir)) {
			Set-ItemProperty -Path 'HKCU:\Environment' -Name 'Path' -Value (($entries + $dir) -join ';') -Type ExpandString
			# Setting a variable through .NET tells running programs, Explorer
			# among them, that the environment changed; the registry alone
			# does not.
			[Environment]::SetEnvironmentVariable('TENJIN_INSTALL_PATH_CHANGED', '1', 'User')
			[Environment]::SetEnvironmentVariable('TENJIN_INSTALL_PATH_CHANGED', $null, 'User')
			Write-Host "Added $dir to your PATH. Open a new terminal to use the tenjin command there."
		}
		if (@($env:Path -split ';' | Where-Object { $_ } | ForEach-Object { Key $_ }) -notcontains (Key $dir)) { $env:Path = "$env:Path;$dir" }

		# Neither a usage event nor an update notice: this run of tenjin is
		# the installer's, not the user's. The variables are put back as they
		# were, since this runs in the user's own shell.
		$saved = $env:TENJIN_NO_TELEMETRY, $env:TENJIN_NO_UPDATE_CHECK
		$env:TENJIN_NO_TELEMETRY, $env:TENJIN_NO_UPDATE_CHECK = '1', '1'
		try { $installed = & $target version } finally { $env:TENJIN_NO_TELEMETRY, $env:TENJIN_NO_UPDATE_CHECK = $saved }
		Write-Host "Installed $installed in $dir"
		# An older copy in a folder of its own, such as one TENJIN_INSTALL_DIR
		# named before, may come first on PATH and run instead of this one.
		$found = Get-Command tenjin -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
		# Compared as full paths: a PATH entry with a trailing slash may find
		# C:\tools\\tenjin.exe, which is this copy.
		if ($found -and -not [string]::Equals([System.IO.Path]::GetFullPath($found.Source), $target, [StringComparison]::OrdinalIgnoreCase)) {
			Write-Host "Another copy of the Tenjin CLI is at $($found.Source), and runs instead of this one. Remove it, or install over it with TENJIN_INSTALL_DIR set to its folder."
			Write-Host "Start with: $target setup"
		} else {
			Write-Host 'Start with: tenjin setup'
		}
	} finally {
		Remove-Item -Path $tmp -Recurse -Force -ErrorAction SilentlyContinue
	}
}
