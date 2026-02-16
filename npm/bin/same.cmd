@echo off
:: SAME — npm wrapper shim (Windows)
:: Delegates to the downloaded Go binary. If missing, triggers install.

set "DIR=%~dp0"
set "BINARY=%DIR%same-binary.exe"

if not exist "%BINARY%" (
  echo [same] Binary not found, downloading... 1>&2
  node "%DIR%..\lib\install.js"
  if not exist "%BINARY%" (
    echo [same] Failed to download binary. Please check your network connection. 1>&2
    echo [same] You can also install manually: https://github.com/sgx-labs/statelessagent/releases 1>&2
    exit /b 1
  )
)

"%BINARY%" %*
