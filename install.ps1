# Installs the Tenjin CLI (the tenjin command) on Windows from its GitHub releases.
#
#   irm https://raw.githubusercontent.com/tenjin/tenjin-cli/main/install.ps1 | iex
#
# TENJIN_VERSION       a version to install, such as 0.1.0 (default: the latest)
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

		$dir = if ($env:TENJIN_INSTALL_DIR) { $env:TENJIN_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'Programs\tenjin' }
		New-Item -ItemType Directory -Path $dir -Force | Out-Null
		$target = Join-Path $dir 'tenjin.exe'
		# Windows cannot overwrite a running program but can rename it, so the
		# old one is moved aside first.
		if (Test-Path $target) {
			Move-Item -Path $target -Destination "$target.old" -Force
		}
		Copy-Item -Path $binary -Destination $target
		Remove-Item -Path "$target.old" -Force -ErrorAction SilentlyContinue

		# The user's PATH is read and written unexpanded, so entries such as
		# %USERPROFILE%\bin stay as they are.
		$key = Get-Item -Path 'HKCU:\Environment'
		$userPath = $key.GetValue('Path', '', 'DoNotExpandEnvironmentNames')
		$entries = @($userPath -split ';' | Where-Object { $_ })
		if ($entries -notcontains $dir) {
			Set-ItemProperty -Path 'HKCU:\Environment' -Name 'Path' -Value (($entries + $dir) -join ';') -Type ExpandString
			# Setting a variable through .NET tells running programs, Explorer
			# among them, that the environment changed; the registry alone
			# does not.
			[Environment]::SetEnvironmentVariable('TENJIN_INSTALL_PATH_CHANGED', '1', 'User')
			[Environment]::SetEnvironmentVariable('TENJIN_INSTALL_PATH_CHANGED', $null, 'User')
			Write-Host "Added $dir to your PATH. Open a new terminal to use the tenjin command there."
		}
		if (($env:Path -split ';') -notcontains $dir) { $env:Path = "$env:Path;$dir" }

		Write-Host "Installed $(& $target version) in $dir"
		Write-Host 'Start with: tenjin setup'
	} finally {
		Remove-Item -Path $tmp -Recurse -Force -ErrorAction SilentlyContinue
	}
}
