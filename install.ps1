# Terminal installer: works from a downloaded release or an HTTPS script.
$ErrorActionPreference = 'Stop'
$buildheadwayRepository = $env:BUILDHEADWAY_RELEASE_REPOSITORY
if (!$buildheadwayRepository) { $buildheadwayRepository = $env:CODENTRAIL_RELEASE_REPOSITORY }
if (!$buildheadwayRepository) { $buildheadwayRepository = $env:FORGE_RELEASE_REPOSITORY }
if (!$buildheadwayRepository) { $buildheadwayRepository = '@RELEASE_REPOSITORY@' }
$buildheadwayVersion = $env:BUILDHEADWAY_VERSION
if (!$buildheadwayVersion) { $buildheadwayVersion = $env:CODENTRAIL_VERSION }
if (!$buildheadwayVersion) { $buildheadwayVersion = $env:FORGE_VERSION }
if (!$buildheadwayVersion) { $buildheadwayVersion = 'latest' }
$buildheadwayTemp = $null
$buildheadwayExit = 0
try {
    $buildheadwayCPU = $env:PROCESSOR_ARCHITECTURE
    if ($env:PROCESSOR_ARCHITEW6432) { $buildheadwayCPU = $env:PROCESSOR_ARCHITEW6432 }
    switch ($buildheadwayCPU) {
        'AMD64' { $buildheadwayArch = 'amd64' }
        'ARM64' { $buildheadwayArch = 'arm64' }
        default { throw 'BuildHeadway requires x64 or ARM64 Windows.' }
    }
    $buildheadwayName = "buildheadway-windows-$buildheadwayArch.exe"
    $buildheadwayRelative = "bin/$buildheadwayName"
    $buildheadwayRoot = $PSScriptRoot
    if (!$buildheadwayRoot -or !(Test-Path -LiteralPath (Join-Path $buildheadwayRoot 'SHA256SUMS') -PathType Leaf)) {
        if ($buildheadwayRepository -notmatch '^[A-Za-z0-9][A-Za-z0-9_.-]*/[A-Za-z0-9][A-Za-z0-9_.-]*$') {
            throw "This installer has no published release repository yet. Use install.ps1 from the downloaded release, or set BUILDHEADWAY_RELEASE_REPOSITORY=OWNER/REPOSITORY after publication."
        }
        [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
        if ($buildheadwayVersion -eq 'latest') {
            $buildheadwayRelease = Invoke-RestMethod -Uri "https://api.github.com/repos/$buildheadwayRepository/releases/latest" -TimeoutSec 120
            if ($buildheadwayRelease.draft -or $buildheadwayRelease.prerelease) { throw 'Expected a stable release.' }
            $buildheadwayVersion = $buildheadwayRelease.tag_name
        }
        if ($buildheadwayVersion -notmatch '^v?\d+\.\d+\.\d+(-[A-Za-z0-9.-]+)?$') { throw 'Invalid release version.' }
        $buildheadwayVersion = 'v' + $buildheadwayVersion.TrimStart('v')
        $buildheadwayTemp = Join-Path ([IO.Path]::GetTempPath()) ('buildheadway-install-' + [Guid]::NewGuid().ToString('N'))
        $buildheadwayRoot = $buildheadwayTemp
        New-Item -ItemType Directory -Path (Join-Path $buildheadwayRoot 'bin') -Force | Out-Null
        $buildheadwayBase = "https://github.com/$buildheadwayRepository/releases/download/$buildheadwayVersion"
        Write-Host "Downloading BuildHeadway $buildheadwayVersion for windows/$buildheadwayArch..."
        Invoke-WebRequest -UseBasicParsing -Uri "$buildheadwayBase/SHA256SUMS" -OutFile (Join-Path $buildheadwayRoot 'SHA256SUMS') -TimeoutSec 180
        Invoke-WebRequest -UseBasicParsing -Uri "$buildheadwayBase/$buildheadwayName" -OutFile (Join-Path $buildheadwayRoot $buildheadwayRelative) -TimeoutSec 180
    }
    $buildheadwayBinary = Join-Path $buildheadwayRoot $buildheadwayRelative
    $buildheadwayManifest = Join-Path $buildheadwayRoot 'SHA256SUMS'
    if (!(Test-Path -LiteralPath $buildheadwayBinary -PathType Leaf)) { throw 'The native BuildHeadway application is missing from this release.' }
    $buildheadwayHashes = @(Get-Content -LiteralPath $buildheadwayManifest | Where-Object { ($_ -split '\s+')[1] -eq $buildheadwayRelative })
    $buildheadwayActual = (Get-FileHash -LiteralPath $buildheadwayBinary -Algorithm SHA256).Hash
    if ($buildheadwayHashes.Count -ne 1 -or ($buildheadwayHashes[0] -split '\s+')[0] -ne $buildheadwayActual) { throw 'BuildHeadway failed integrity verification. Download the release again.' }
    $buildheadwayPreviousRepo = $env:BUILDHEADWAY_RELEASE_REPOSITORY
    try {
        if ($buildheadwayRepository -match '^[A-Za-z0-9][A-Za-z0-9_.-]*/[A-Za-z0-9][A-Za-z0-9_.-]*$') { $env:BUILDHEADWAY_RELEASE_REPOSITORY = $buildheadwayRepository }
        & $buildheadwayBinary install @args
        $buildheadwayExit = $LASTEXITCODE
    } finally { $env:BUILDHEADWAY_RELEASE_REPOSITORY = $buildheadwayPreviousRepo }
} catch {
    Write-Error -ErrorAction Continue $_
    $buildheadwayExit = 2
} finally {
    if ($buildheadwayTemp -and (Test-Path -LiteralPath $buildheadwayTemp)) { Remove-Item -LiteralPath $buildheadwayTemp -Recurse -Force }
}
if ($buildheadwayExit -ne 0) { throw "BuildHeadway installation failed (exit $buildheadwayExit)." }
