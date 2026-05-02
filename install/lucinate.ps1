#Requires -Version 5.1
$ErrorActionPreference = 'Stop'

$Repo = 'lucinate-ai/lucinate'
$Binary = 'lucinate'
$InstallDir = Join-Path $env:LOCALAPPDATA "Programs\$Binary"

$Arch = switch ($env:PROCESSOR_ARCHITECTURE) {
    'AMD64' { 'amd64' }
    'ARM64' { 'arm64' }
    'x86'   { 'amd64' }
    default { throw "Unsupported architecture: $($env:PROCESSOR_ARCHITECTURE)" }
}

$Os = 'windows'
$Ext = 'zip'

$Headers = @{ 'User-Agent' = "$Binary-installer" }
$Release = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/latest" -Headers $Headers
$Tag = $Release.tag_name
$Version = $Tag.TrimStart('v')

$Asset = "${Binary}_${Version}_${Os}_${Arch}.${Ext}"
$Url = "https://github.com/$Repo/releases/download/$Tag/$Asset"

$TmpDir = Join-Path ([System.IO.Path]::GetTempPath()) ([System.IO.Path]::GetRandomFileName())
New-Item -ItemType Directory -Path $TmpDir | Out-Null

try {
    Write-Host "Downloading $Binary $Version for $Os/$Arch..."
    $ArchivePath = Join-Path $TmpDir $Asset
    Invoke-WebRequest -Uri $Url -OutFile $ArchivePath -Headers $Headers -UseBasicParsing
    Expand-Archive -Path $ArchivePath -DestinationPath $TmpDir -Force

    if (-not (Test-Path $InstallDir)) {
        New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
    }

    $SourceBinary = Join-Path $TmpDir "$Binary.exe"
    $DestBinary = Join-Path $InstallDir "$Binary.exe"
    Copy-Item -Path $SourceBinary -Destination $DestBinary -Force

    $UserPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    $PathEntries = if ($UserPath) { $UserPath.Split(';') } else { @() }
    if ($PathEntries -notcontains $InstallDir) {
        $NewPath = if ($UserPath) { "$UserPath;$InstallDir" } else { $InstallDir }
        [Environment]::SetEnvironmentVariable('Path', $NewPath, 'User')
        Write-Host "Added $InstallDir to user PATH (restart your shell to pick it up)."
    }

    Write-Host "$Binary $Version installed to $DestBinary"
}
finally {
    Remove-Item -Recurse -Force -Path $TmpDir -ErrorAction SilentlyContinue
}
