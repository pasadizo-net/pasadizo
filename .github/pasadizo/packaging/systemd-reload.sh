#!/bin/sh
# Let systemd read the pasadizo units again after the package changes them.
set -e

if [ -d /run/systemd/system ]; then
	systemctl --system daemon-reload >/dev/null || true
fi
