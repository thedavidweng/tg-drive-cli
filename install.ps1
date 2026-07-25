$ErrorActionPreference = "Stop"
$Repo = "thedavidweng/tg-drive-cli"
$Binary = "td"

function Step($msg) { Write-Host "==> $msg" }
function Die($msg) { Write-Error "ERROR: $msg"; exit 1 }

$arch = if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") { "arm64" } else { "x86_64" }
$version = (Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/latest").tag_name
$asset = "${Binary}_windows_${arch}.zip"
$url = "https://github.com/$Repo/releases/download/$version/$asset"
$installDir = if ($env:TD_INSTALL_DIR) { $env:TD_INSTALL_DIR } else { Join-Path (Join-Path $env:LOCALAPPDATA "tg-drive-cli") "bin" }
$tmpDir = Join-Path $env:TEMP "tg-drive-cli-install-$([guid]::NewGuid().ToString('N').Substring(0,8))"
New-Item -ItemType Directory -Path $tmpDir -Force | Out-Null
New-Item -ItemType Directory -Path $installDir -Force | Out-Null
try {
  Step "Downloading $asset"
  $zipPath = Join-Path $tmpDir $asset
  Invoke-WebRequest -Uri $url -OutFile $zipPath -UseBasicParsing
  Expand-Archive -Path $zipPath -DestinationPath $tmpDir -Force
  Copy-Item (Join-Path $tmpDir "$Binary.exe") (Join-Path $installDir "$Binary.exe") -Force
  $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
  if ($userPath -notlike "*$installDir*") {
    [Environment]::SetEnvironmentVariable("Path", "$installDir;$userPath", "User")
  }
  Step "Installed $Binary $version"
} finally {
  Remove-Item -Path $tmpDir -Recurse -Force -ErrorAction SilentlyContinue
}
Step "Run 'td auth login' to get started."
