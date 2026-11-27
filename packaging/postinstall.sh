#!/bin/sh
set -e

if [ "$1" = configure ]; then
	systemd-sysusers /usr/lib/sysusers.d/phev2mqtt.conf
	chgrp phev2mqtt /etc/phev2mqtt/config.yaml
	chmod 0640 /etc/phev2mqtt/config.yaml

	if [ -d /run/systemd/system ]; then
		systemctl daemon-reload
		# Upgrade: pick up the new binary. A fresh install is not started
		# until the config is filled in and the service enabled.
		systemctl try-restart phev2mqtt.service
	fi
fi
