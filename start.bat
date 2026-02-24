@echo off
chcp 65001 >nul
title Sakura Bot

if not exist "venv\" (
    echo [INFO] Creating virtual environment...
    python -m venv venv
    if errorlevel 1 (
        echo [ERROR] Failed to create virtual environment!
        pause
        exit /b 1
    )
    echo [SUCCESS] Virtual environment created
    echo.
)

echo [INFO] Activating virtual environment...
call venv\Scripts\activate.bat
if errorlevel 1 (
    echo [ERROR] Failed to activate virtual environment!
    pause
    exit /b 1
)

echo [INFO] Checking dependencies...
python -c "import telegram" 2>nul
if errorlevel 1 (
    echo [WARNING] Dependencies not installed, installing...
    pip install -r requirements.txt
    if errorlevel 1 (
        echo [ERROR] Failed to install dependencies!
        pause
        exit /b 1
    )
    echo [SUCCESS] Dependencies installed
    echo.
)

echo [INFO] Starting Sakura Bot...
echo.
python main.py

echo.
echo [INFO] Program exited
pause
