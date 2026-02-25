@echo off
chcp 65001 >nul
title Sakura Bot

:: Set virtual environment Python path
set "PYTHON_EXE=venv\Scripts\python.exe"
set "PIP_EXE=venv\Scripts\pip.exe"

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

:: Verify virtual environment Python exists
if not exist "%PYTHON_EXE%" (
    echo [ERROR] Virtual environment Python not found at: %PYTHON_EXE%
    pause
    exit /b 1
)

echo [INFO] Using virtual environment Python: %PYTHON_EXE%

echo [INFO] Checking dependencies...
"%PYTHON_EXE%" -c "import telegram" 2>nul
if errorlevel 1 (
    echo [WARNING] Dependencies not installed, installing...
    "%PIP_EXE%" install -r requirements.txt
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
"%PYTHON_EXE%" main.py

echo.
echo [INFO] Program exited
pause
