#!/bin/bash
set -e

echo "Running tests with coverage..."
go test -v -coverprofile=coverage.out ./...
echo "Generating HTML coverage report..."
go tool cover -html=coverage.out -o coverage.html
echo "Coverage report generated: coverage.html"
