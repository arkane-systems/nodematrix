#!/bin/sh
# Runs before the package's files are removed. dpkg passes "remove" for a
# real removal/purge, or "upgrade" when a new version is about to be
# unpacked - in the upgrade case we deliberately leave the service running,
# since postinst's `systemctl restart` will hand it the new binary.
set -e

if [ "$1" = "remove" ]; then
    systemctl stop nodematrix.service >/dev/null 2>&1 || true
    systemctl disable nodematrix.service >/dev/null 2>&1 || true
fi

exit 0
