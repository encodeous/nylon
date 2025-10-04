#!/bin/bash
set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Detect if terminal supports colors
if [ -t 1 ] && command -v tput &> /dev/null && tput colors &> /dev/null && [ $(tput colors) -ge 8 ]; then
    USE_COLOR=true
else
    USE_COLOR=false
fi

print_status() {
    if [ "$USE_COLOR" = true ]; then
        echo -e "${GREEN}✓${NC} $1"
    else
        echo "✓ $1"
    fi
}

print_error() {
    if [ "$USE_COLOR" = true ]; then
        echo -e "${RED}✗${NC} $1"
    else
        echo "✗ $1"
    fi
}

print_info() {
    if [ "$USE_COLOR" = true ]; then
        echo -e "${YELLOW}ℹ${NC} $1"
    else
        echo "ℹ $1"
    fi
}

# Change to script directory
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

# Check if nylon binary exists
NYLON_BINARY="../nylon"
if [ ! -f "$NYLON_BINARY" ]; then
    print_error "Nylon binary not found at $NYLON_BINARY"
    print_info "Please build the binary first: go build -o nylon"
    exit 1
fi

# Make nylon binary executable
chmod +x "$NYLON_BINARY"

print_info "Running smoke tests for Nylon..."
echo ""

# Test 1: Check that nylon executes and shows help
print_info "Test 1: Checking if nylon executes and shows help text..."

CONTAINER_NAME="nylon-smoke-test-1-$$"
docker run --rm --name "$CONTAINER_NAME" \
    -v "$(cd .. && pwd)/nylon:/nylon:ro" \
    busybox:1.37-glibc \
    /nylon > /tmp/nylon-output-1.txt 2>&1 || true

if grep -q "Nylon is a mesh networking system." /tmp/nylon-output-1.txt; then
    print_status "Test 1 PASSED: Nylon executes and shows help text"
else
    print_error "Test 1 FAILED: Expected help text not found"
    cat /tmp/nylon-output-1.txt
    rm -f /tmp/nylon-output-1.txt
    exit 1
fi
rm -f /tmp/nylon-output-1.txt
echo ""

# Test 2: Check that nylon runs with config files
print_info "Test 2: Checking if nylon runs with config files..."

CONTAINER_NAME="nylon-smoke-test-2-$$"
CONTAINER_ID=$(docker run -d --name "$CONTAINER_NAME" \
    --cap-add=NET_ADMIN \
    -v "$(cd .. && pwd)/nylon:/nylon:ro" \
    -v "$SCRIPT_DIR/fixtures/testcentral1.yaml:/central.yaml:ro" \
    -v "$SCRIPT_DIR/fixtures/testnode1.yaml:/node.yaml:ro" \
    busybox:1.37-glibc \
    sh -c "mkdir -p /dev/net && mknod /dev/net/tun c 10 200 && /nylon -c /central.yaml -n /node.yaml run")

# Wait for the expected log message (with timeout)
TIMEOUT=10
ELAPSED=0
SUCCESS=false

while [ $ELAPSED -lt $TIMEOUT ]; do
    if docker logs "$CONTAINER_ID" 2>&1 | grep -q "Nylon has been initialized. To gracefully exit, send SIGINT or Ctrl+C."; then
        SUCCESS=true
        break
    fi
    sleep 1
    ELAPSED=$((ELAPSED + 1))
done

# Cleanup
docker stop "$CONTAINER_ID" > /dev/null 2>&1 || true
docker rm "$CONTAINER_ID" > /dev/null 2>&1 || true

if [ "$SUCCESS" = true ]; then
    print_status "Test 2 PASSED: Nylon runs successfully with config files"
else
    print_error "Test 2 FAILED: Expected initialization message not found within ${TIMEOUT}s"
    docker logs "$CONTAINER_ID" 2>&1 || true
    exit 1
fi
echo ""

print_status "All smoke tests passed!"
exit 0

