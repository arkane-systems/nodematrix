# nodematrix

A Matrix-style digital rain console screensaver for Kubernetes nodes. It
runs on a node's physical or virtual VGA console (iDRAC/iLO virtual KVM, a
hypervisor console, or a directly attached monitor — not a serial console),
replacing the idle `login:` prompt with an animated display, then getting
out of the way on any keypress.

- Activates after a configurable period of console inactivity.
- Dismisses on any keypress, without passing that keypress through to
  `login`.
- Preserves whatever was on the console underneath it exactly, including
  any messages that arrive while it's hidden.
- Shows a bordered status panel (hostname, uptime, load, memory, disk,
  kubelet status) and a flag indicating whether console output occurred
  while it was hidden.

## Deployment prerequisite: `console=ttyN` kernel parameter

**Before installing, confirm the node's kernel command line sets an
explicit, numbered console, e.g. `console=tty1` — not `console=tty0`, and
not omitted entirely.**

Without this, the kernel routes console output through `tty0` — an alias
for "whichever VT is currently active" — rather than a fixed VT. The moment
nodematrix switches the foreground VT away to show the screensaver, console
messages would start printing onto the screensaver's own display instead of
being preserved on the login VT, defeating the core guarantee this project
exists to provide.

Check with:

```bash
cat /proc/consoles
```

If this shows `tty0` rather than a specific number, apply an explicit
`console=tty1` kernel parameter (e.g. via a `GRUB_CMDLINE_LINUX_DEFAULT`
change and `update-grub`/`grub2-mkconfig`) and **reboot** — this is a
boot-time kernel parameter and cannot be changed at runtime. `nodematrix`
also checks this itself at startup and logs a warning if it looks wrong,
but it will still run either way; this is a prerequisite specifically for
the preservation guarantee to actually hold.

## Installing

Install the `.deb` package (see [Building](#building) if you need to build
one):

```bash
sudo apt install ./nodematrix_<version>_amd64.deb
```

This installs the binary to `/usr/sbin/nodematrix`, installs and enables
the `nodematrix.service` systemd unit, and starts it immediately. Removing
the package (`apt remove nodematrix`) stops and disables the service first;
purging (`apt purge nodematrix`) also cleans up its runtime lock file.

The package does **not** and cannot apply the `console=ttyN` kernel
parameter above — that's a separate, boot-time prerequisite outside any
package's normal file/service installation.

## Configuration

`nodematrix` is configured via command-line flags, set in
`/lib/systemd/system/nodematrix.service`'s `ExecStart=` line (or an override
via `systemctl edit nodematrix`):

| Flag | Default | Meaning |
| --- | --- | --- |
| `-login-vt` | `1` | The VT running login, to hide/reveal. |
| `-screensaver-vt` | `63` | The VT the screensaver renders on (falls back to a dynamically-allocated VT if unavailable). |
| `-idle-timeout` | `5m` | How long the login console must be idle before the screensaver activates. |
| `-tick-rate` | `100ms` | Animation frame interval (rounded to the nearest 0.1s). |
| `-status-interval` | `5s` | How often status content (uptime, load, etc.) is refreshed while showing. |
| `-hostname` | (system hostname) | Hostname shown in the frame title. |
| `-enable-kana` | `false` | Include halfwidth katakana in the rain. Off by default — not every console font has these glyphs drawn (Debian's default font and even GNU Unifont as currently packaged don't); enable only once confirmed working on the target console. |

Must run as root (VT ioctls and `/dev/input` access both require it) — the
systemd unit already runs it that way.

## Building

Requires Go and (for the `.deb`) [nfpm](https://nfpm.goreleaser.com/):

```bash
make deb VERSION=0.1.0
```

This produces `nodematrix_<version>_amd64.deb` in the repo root. `make build`
alone just builds the binary into `dist/`; `make clean` removes both.

## Project layout

- [`internal/render`](internal/render) — the digital rain effect and status
  frame, as a display-only package with no privileged/OS-specific
  dependencies, so it can be developed and unit-tested independently. See
  [`cmd/render-demo`](cmd/render-demo) to watch it run in an ordinary
  terminal.
- [`internal/daemon`](internal/daemon) — VT switching, evdev-based idle
  detection, the console-activity monitor, and status collection.
- [`cmd/nodematrix`](cmd/nodematrix) — the daemon entrypoint.
- [`cmd/vtspike`](cmd/vtspike) — a standalone diagnostic that validates the
  VT-switch/evdev/vcs assumptions this design depends on, on real hardware.
- [`systemd/nodematrix.service`](systemd/nodematrix.service),
  [`packaging/`](packaging) — the systemd unit and `.deb` packaging.
