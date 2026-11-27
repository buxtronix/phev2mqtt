#!/bin/sh
set -e

if [ "$1" = remove ] && [ -d /run/systemd/system ]; then
	systemctl disable --now phev2mqtt.service || true
fi
