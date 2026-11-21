# GCSE Revision — one-command install for Windows (PowerShell):
#   irm https://raw.githubusercontent.com/gautampachnanda101/gcse-revision-assistant/main/install.ps1 | iex
# Installs a single self-contained program (no admin rights needed), adds it to your PATH, then starts it.
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

$Repo = if ($env:GCSE_REVISION_REPO) { $env:GCSE_REVISION_REPO } else { 'gautampachnanda101/gcse-revision-assistant' }
$Name = 'gcse-revision'
$Arch = if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { 'arm64' } else { 'amd64' }
$Asset = "$Name-windows-$Arch.exe"
$Base = if ($env:GCSE_REVISION_DOWNLOAD_BASE) { $env:GCSE_REVISION_DOWNLOAD_BASE } else { "https://github.com/$Repo/releases/latest/download" }
$Dir = Join-Path $env:LOCALAPPDATA "Programs\$Name"
New-Item -ItemType Directory -Force -Path $Dir | Out-Null
$Exe = Join-Path $Dir "$Name.exe"
$Tmp = "$Exe.download"

Write-Host "Downloading $Asset..."
Invoke-WebRequest -UseBasicParsing "$Base/$Asset" -OutFile $Tmp
$Got = ([BitConverter]::ToString([Security.Cryptography.SHA256]::Create().ComputeHash([IO.File]::ReadAllBytes($Tmp))) -replace '-', '').ToLower()
$Want = $null
for ($attempt = 1; $attempt -le 5; $attempt++) {
  $Sums = (Invoke-WebRequest -UseBasicParsing "$Base/checksums.txt").Content
  $Want = ($Sums -split "`n" | Where-Object { $_ -match "\s$([regex]::Escape($Asset))\s*$" } | ForEach-Object { ($_ -split '\s+')[0] }) | Select-Object -First 1
  if ($Want -and $Want.ToLower() -eq $Got) { break }
  if ($attempt -lt 5) { Start-Sleep -Seconds 2 }
}
if (-not $Want -or $Want.ToLower() -ne $Got) {
  Remove-Item $Tmp -Force
  throw "Checksum check failed for $Asset (expected $Want, got $Got). Try again."
}
Move-Item -Force $Tmp $Exe
Write-Host "Installed $Exe"

$UserPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if ($env:GCSE_REVISION_NO_PATH -ne '1' -and ($UserPath -split ';') -notcontains $Dir) {
  [Environment]::SetEnvironmentVariable('Path', ($UserPath.TrimEnd(';') + ";$Dir").TrimStart(';'), 'User')
  Write-Host "Added $Dir to your PATH (open a new terminal to use '$Name')."
}

if ($env:GCSE_REVISION_NO_RUN -ne '1') {
  Write-Host "Starting GCSE Revision... (next time just run: $Name)"
  & $Exe
}
