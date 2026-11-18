@echo off
setlocal
cd /d "%~dp0" || exit /b 1

if not exist "data\.env" (
    copy /Y "data\.env.example" "data\.env" >nul || exit /b 1
    echo Configure Telegram, MySQL, and AI in data\.env, then run this script again.
    exit /b 1
)

call npm --prefix web ci || exit /b 1
call npm --prefix web run build || exit /b 1
if not exist "bin" mkdir "bin" || exit /b 1
set /p "SAKURA_BUILD_VERSION="<VERSION
go build -trimpath -ldflags="-X github.com/Sakura520222/Sakura-Bot/internal/version.Version=%SAKURA_BUILD_VERSION%" -o bin/ ./cmd/sakura-main ./cmd/sakura-qa || exit /b 1
bin\sakura-main.exe %*
exit /b %errorlevel%
