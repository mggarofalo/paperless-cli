# Build from this checkout and install for the current Windows user.
[CmdletBinding()]
param(
    [string]$InstallDir = (Join-Path $HOME '.local\bin')
)

$ErrorActionPreference = 'Stop'
$InstallDir = [IO.Path]::GetFullPath($InstallDir)
$binaryPath = Join-Path $InstallDir 'paperless.exe'

Get-Command go -ErrorAction Stop | Out-Null
New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
Push-Location $PSScriptRoot
try {
    go build -o $binaryPath .
    if ($LASTEXITCODE -ne 0) { throw 'Building paperless failed.' }
} finally {
    Pop-Location
}

function Test-PathEntry([string]$PathValue, [string]$Directory) {
    foreach ($entry in ($PathValue -split ';')) {
        $expanded = [Environment]::ExpandEnvironmentVariables($entry.Trim().Trim('"'))
        if ($expanded.TrimEnd('\', '/') -ieq $Directory.TrimEnd('\', '/')) {
            return $true
        }
    }
    return $false
}

# Preserve all existing entries. Never use setx, which can truncate PATH.
$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if (-not (Test-PathEntry $userPath $InstallDir)) {
    $updatedPath = if ([string]::IsNullOrEmpty($userPath)) { $InstallDir } else { "$userPath;$InstallDir" }
    [Environment]::SetEnvironmentVariable('Path', $updatedPath, 'User')
    Write-Host 'Added install directory to your user PATH. Restart existing terminals/apps to refresh their environment.'
}
if (-not (Test-PathEntry $env:Path $InstallDir)) {
    $env:Path = "$InstallDir;$env:Path"
}

Write-Host "Installed $binaryPath"
& $binaryPath --version
if ($LASTEXITCODE -ne 0) { throw 'Installed binary verification failed.' }
