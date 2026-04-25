echo off
setlocal

echo === CCTV Video Downloader Build Script ===

:: Basic configuration
set BINARY_NAME=cctvdown
set MAIN_PATH=./cmd/cctvdown/dist
set VERSION=0.1.0


:: Build
echo Building project...
go build -ldflags "-s -w -X main.Version=%VERSION%" -o %BINARY_NAME%.exe %MAIN_PATH%

if %ERRORLEVEL% neq 0 (
    echo Build failed!
    exit /b 1
)

echo Build successful: %BINARY_NAME%.exe

:: Show file size
for %%A in (%BINARY_NAME%.exe) do echo File size: %%~zA bytes

echo.
echo Build process completed.
