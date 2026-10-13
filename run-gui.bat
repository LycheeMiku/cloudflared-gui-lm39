@echo off
setlocal
cd /d "%~dp0"

REM Build executable if not present
if not exist "cloudflared.exe" (
    echo Compiling cloudflared native GUI...
    set CGO_ENABLED=1
    go build -o cloudflared.exe ./cmd/cloudflared
    if errorlevel 1 (
        echo [ERROR] Failed to compile cloudflared.
        pause
        exit /b 1
    )
)

REM Launch completely silently without terminal window
if exist "run-gui.vbs" (
    start "" wscript "%~dp0run-gui.vbs"
) else (
    start "" /b cloudflared.exe gui %*
)

endlocal
