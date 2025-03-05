#!/bin/bash
set -e

INSTANCE_NAME="nylon-integration-runner"
DISTRO="ubuntu:22.04"

if lxc info "$INSTANCE_NAME" &> /dev/null; then
    echo "Instance $INSTANCE_NAME already exists, removing it"
    lxc rm --force "$INSTANCE_NAME"
fi

echo "Launching LXD VM instance: $INSTANCE_NAME"
lxc launch "$DISTRO" "$INSTANCE_NAME" --vm

echo "Waiting for $INSTANCE_NAME to become RUNNING..."
while true; do
    status=$(lxc exec "$INSTANCE_NAME" -- systemctl status cron 2>&1) || true
    if [[ "$status" == *"loaded"* ]]; then
        break
    fi
    sleep 1
done
echo "$INSTANCE_NAME is running."

echo "Installing Docker..."
lxc exec "$INSTANCE_NAME" -- bash -c "apt-get update && apt-get install -y curl"
lxc exec "$INSTANCE_NAME" -- bash -c "curl -sL get.docker.io | sh"

lxc exec "$INSTANCE_NAME" -- bash -c "mkdir -p /etc/docker && echo '{\"hosts\": [\"tcp://0.0.0.0:2375\", \"unix:///var/run/docker.sock\"]}' > /etc/docker/daemon.json"
lxc exec "$INSTANCE_NAME" -- bash -c "mkdir -p /etc/systemd/system/docker.service.d && echo -e '[Service]\nExecStart=\nExecStart=/usr/bin/dockerd' > /etc/systemd/system/docker.service.d/override.conf"
lxc exec "$INSTANCE_NAME" -- systemctl daemon-reload
lxc exec "$INSTANCE_NAME" -- systemctl restart docker.service

echo "Integration runner initialization complete."
