#!/bin/sh
# Runs after the package's files are unpacked (dpkg calls this
# "postinst configure <old-version>" on both fresh installs and upgrades).
set -e

if [ "$1" = "configure" ]; then
    systemctl daemon-reload >/dev/null 2>&1 || true
    systemctl enable nodematrix.service >/dev/null 2>&1 || true
    # restart (rather than start) covers both a fresh install (no-op
    # difference, the unit wasn't running) and an upgrade (picks up the new
    # binary) with one call.
    systemctl restart nodematrix.service >/dev/null 2>&1 || true
fi

exit 0
