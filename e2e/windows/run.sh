#!/usr/bin/env bash
set -euo pipefail

image=$(realpath "${1:?Usage: e2e/windows/run.sh IMAGE.qcow2}")
cd "$(dirname "$0")/../.."
mkdir -p workdir/e2e/windows

docker build --target debug -t nylon-debug:latest .
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o workdir/e2e/windows/nylon.exe .
curl -fsSL https://www.wintun.net/builds/wintun-0.14.1.zip -o workdir/e2e/windows/wintun.zip
unzip -p workdir/e2e/windows/wintun.zip wintun/bin/amd64/wintun.dll > workdir/e2e/windows/wintun.dll
python3 -m venv workdir/e2e/windows/venv
workdir/e2e/windows/venv/bin/pip install --quiet pywinrm==0.5.0
exec workdir/e2e/windows/venv/bin/python e2e/windows/connectivity.py "$image"
