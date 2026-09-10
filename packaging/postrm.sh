#!/bin/sh
# Runs after the package's files are removed.
set -e

case "$1" in
    remove|purge)
        systemctl daemon-reload >/dev/null 2>&1 || true
        ;;
esac

if [ "$1" = "purge" ]; then
    # /run is tmpfs and clears on reboot regardless; removing it here just
    # tidies up immediately rather than leaving it until the next boot.
    rm -f /run/nodematrix.lock 2>/dev/null || true
fi

exit 0
