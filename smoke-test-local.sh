#!/bin/bash
set -e

VM_IP=$(lxc list nylon-smoke-runner --format csv -c 4 | grep "enp5s0" | awk '{print $1}')
export DOCKER_HOST="tcp://$VM_IP:2375"
go test -v -tags=smoke ./smoke/...
