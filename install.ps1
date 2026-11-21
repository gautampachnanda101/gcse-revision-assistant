# GCSE Revision — one-command install for Windows (PowerShell):
#   irm https://raw.githubusercontent.com/gautampachnanda101/gcse-revision-assistant/main/install.ps1 | iex
# Installs a single self-contained program (no admin rights needed), adds it to your PATH and registers a user background task.
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
$TaskName = 'GCSERevision'
$HadTask = $false
try {
  & schtasks /Query /TN $TaskName *> $null
  $HadTask = $LASTEXITCODE -eq 0
} catch {
  $HadTask = $false
}

Write-Host "Downloading $Asset..."
Invoke-WebRequest -UseBasicParsing "$Base/$Asset" -OutFile $Tmp
$Got = ([BitConverter]::ToString([Security.Cryptography.SHA256]::Create().ComputeHash([IO.File]::ReadAllBytes($Tmp))) -replace '-', '').ToLower()
$Want = $null
$SumsFile = "$Tmp.checksums"
for ($attempt = 1; $attempt -le 5; $attempt++) {
  Invoke-WebRequest -UseBasicParsing "$Base/checksums.txt" -OutFile $SumsFile
  $Sums = Get-Content -Raw $SumsFile
  $Want = ($Sums -split "`n" | Where-Object { $_ -match "\s$([regex]::Escape($Asset))\s*$" } | ForEach-Object { ($_ -split '\s+')[0] }) | Select-Object -First 1
  if ($Want -and $Want.ToLower() -eq $Got) { break }
  if ($attempt -lt 5) { Start-Sleep -Seconds 2 }
}
Remove-Item $SumsFile -Force -ErrorAction SilentlyContinue
if (-not $Want -or $Want.ToLower() -ne $Got) {
  Remove-Item $Tmp -Force
  throw "Checksum check failed for $Asset (expected $Want, got $Got). Try again."
}
if ($HadTask) {
  & schtasks /End /TN $TaskName *> $null
  Start-Sleep -Seconds 2
}
try {
  for ($attempt = 1; $attempt -le 5; $attempt++) {
    try { Move-Item -Force $Tmp $Exe; break }
    catch {
      if ($attempt -eq 5) { throw }
      Start-Sleep -Seconds 1
    }
  }
} catch {
  if ($HadTask) { & schtasks /Run /TN $TaskName *> $null }
  throw
}
if ($HadTask) { & schtasks /Run /TN $TaskName *> $null }
Write-Host "Installed $Exe"

$UserPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if ($env:GCSE_REVISION_NO_PATH -ne '1' -and ($UserPath -split ';') -notcontains $Dir) {
  [Environment]::SetEnvironmentVariable('Path', ($UserPath.TrimEnd(';') + ";$Dir").TrimStart(';'), 'User')
  Write-Host "Added $Dir to your PATH (open a new terminal to use '$Name')."
}

if ($env:GCSE_REVISION_NO_RUN -ne '1') {
  Write-Host "Starting GCSE Revision as a background service..."
  & $Exe service install
  if ($LASTEXITCODE -ne 0) { throw 'Background service setup failed' }
  Start-Process 'http://gcse-revision.localhost:8765/'
  Write-Host "Manage it with: $Name service status|restart|logs|uninstall"
}
