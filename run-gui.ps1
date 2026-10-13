# Cloudflared Native Desktop GUI Launcher
$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $ScriptDir

$env:CGO_ENABLED = "1"

if (-not (Test-Path "$ScriptDir\cloudflared.exe")) {
    Write-Host "Compiling native cloudflared desktop binary..." -ForegroundColor Cyan
    & go build -o cloudflared.exe ./cmd/cloudflared
    if ($LASTEXITCODE -ne 0) {
        Write-Error "Failed to build cloudflared."
        exit 1
    }
}

# Launch native GUI without displaying terminal window
Start-Process -FilePath "$ScriptDir\cloudflared.exe" -ArgumentList @("gui", $args) -WindowStyle Hidden
