#!/bin/sh
# nylon installer
# Installs the latest nylon release to /usr/local/bin as well as a systemd
# unit to enable nylon as a service. opt out with --no-service
# Re-running upgrades nylon and restarts the service. Tested on Ubuntu 24.04.

set -eu

REPO="encodeous/nylon"
BIN="/usr/local/bin/nylon"
CONFIG_DIR="/etc/nylon"
UNIT="/etc/systemd/system/nylon.service"

usage() {
	cat <<EOF
Usage: install.sh [--version <tag>] [--no-service] [--uninstall]

  --version <tag>  Install a specific release, e.g. v0.4.6 (default: latest)
  --no-service     Don't install the systemd service that runs nylon with the
                   configs in $CONFIG_DIR (installed by default)
  --uninstall      Remove the service and binary (keeps $CONFIG_DIR)
EOF
}

info() { echo "==> $*" >&2; }
die() {
	echo "error: $*" >&2
	exit 1
}

as_root() {
	if [ "$(id -u)" = 0 ]; then
		"$@"
	elif command -v sudo >/dev/null 2>&1; then
		sudo "$@"
	else
		die "need root to run: $*"
	fi
}

detect_arch() {
	case "$(uname -m)" in
	x86_64 | amd64) ARCH=amd64 ;;
	aarch64 | arm64) ARCH=arm64 ;;
	i386 | i686) ARCH=386 ;;
	*) die "unsupported architecture: $(uname -m)" ;;
	esac
}

# resolves latest to a tag
resolve_version() {
	case "$VERSION" in
	latest)
		VERSION="$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$REPO/releases/latest" | sed -n 's#.*/tag/##p')"
		[ -n "$VERSION" ] || die "could not determine the latest release"
		;;
	v*) ;;
	*) VERSION="v$VERSION" ;;
	esac
}

installed_version() {
	if [ -x "$BIN" ]; then
		"$BIN" version 2>/dev/null | sed -n 's/^Version: //p'
	fi
}

download_and_install() {
	asset="nylon-linux-$ARCH.tar.gz"
	base="https://github.com/$REPO/releases/download/$VERSION"

	info "downloading $asset ($VERSION)"
	curl -fsSL -o "$TMP/$asset" "$base/$asset" || die "failed to download $base/$asset"

	# check md5 for releases predating this pr (they didn't publish sha256)
	if curl -fsSL -o "$TMP/sum" "$base/$asset.sha256" 2>/dev/null; then
		actual="$(sha256sum "$TMP/$asset" | cut -d' ' -f1)"
	elif curl -fsSL -o "$TMP/sum" "$base/$asset.md5" 2>/dev/null; then
		actual="$(md5sum "$TMP/$asset" | cut -d' ' -f1)"
	else
		die "no checksum published for $asset; refusing to install"
	fi
	[ "$(cut -d' ' -f1 <"$TMP/sum")" = "$actual" ] || die "checksum mismatch for $asset"

	tar -xzf "$TMP/$asset" -C "$TMP" nylon
	"$TMP/nylon" version >/dev/null 2>&1 ||
		die "the downloaded binary does not run on this system (if $(dirname "$TMP") is mounted noexec, set TMPDIR to another directory)"

	# atomic replace
	as_root install -m 0755 "$TMP/nylon" "$BIN.new"
	as_root mv -f "$BIN.new" "$BIN"
	info "installed $BIN ($VERSION)"
}

MARKER="# Managed by nylon's install.sh"

service_running() {
	systemctl is-active --quiet nylon
}

# unit is only managed if it was installed by this script
managed_unit() {
	grep -qF "$MARKER" "$UNIT" 2>/dev/null
}

install_service() {
	cat >"$TMP/unit" <<EOF
$MARKER; use 'systemctl edit nylon' for local changes.
[Unit]
Description=nylon mesh network
Documentation=https://nylon.jq.ax
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=$BIN run -c $CONFIG_DIR/central.yaml -n $CONFIG_DIR/node.yaml
WorkingDirectory=$CONFIG_DIR
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF
	as_root mkdir -p "$CONFIG_DIR"
	as_root chmod 0700 "$CONFIG_DIR"
	changed=0
	if ! cmp -s "$TMP/unit" "$UNIT"; then
		as_root install -m 0644 "$TMP/unit" "$UNIT"
		as_root systemctl daemon-reload
		changed=1
	fi
	as_root systemctl enable --quiet nylon

	if ! as_root test -f "$CONFIG_DIR/node.yaml"; then
		info "nylon.service is enabled. Put node.yaml (and central.yaml, unless you use"
		info "config distribution) in $CONFIG_DIR, then run: sudo systemctl start nylon"
		return
	elif ! service_running; then
		as_root systemctl start nylon
		info "started nylon.service"
	elif [ "$UPGRADED" = 1 ] || [ "$changed" = 1 ]; then
		as_root systemctl restart nylon
		info "restarted nylon.service"
	else
		info "nylon.service is already running"
		return
	fi
	info "check on it with: systemctl status nylon (logs: journalctl -u nylon -f)"
}

uninstall() {
	if managed_unit; then
		as_root systemctl disable --now --quiet nylon || true
		as_root rm -f "$UNIT"
		as_root systemctl daemon-reload
		info "removed nylon.service"
	fi
	if [ -e "$BIN" ]; then
		as_root rm -f "$BIN"
		info "removed $BIN"
	fi
	if as_root test -d "$CONFIG_DIR"; then
		info "kept $CONFIG_DIR, which holds your private key; remove it with: sudo rm -r $CONFIG_DIR"
	fi
}

main() {
	VERSION=latest
	SERVICE=1
	UNINSTALL=0
	while [ $# -gt 0 ]; do
		case "$1" in
		--version)
			[ $# -ge 2 ] || die "--version needs a tag"
			VERSION="$2"
			shift
			;;
		--service) SERVICE=1 ;;
		--no-service) SERVICE=0 ;;
		--uninstall) UNINSTALL=1 ;;
		-h | --help)
			usage
			exit 0
			;;
		*) die "unknown option: $1 (see --help)" ;;
		esac
		shift
	done

	[ "$(uname -s)" = Linux ] || die "this script supports Linux only; see https://github.com/$REPO/releases"
	if [ "$UNINSTALL" = 1 ]; then
		uninstall
		exit 0
	fi
	if [ "$SERVICE" = 1 ] && [ ! -d /run/systemd/system ]; then
		info "systemd isn't running; installing the binary only"
		SERVICE=0
	fi
	if [ "$SERVICE" = 1 ] && [ -f "$UNIT" ] && ! managed_unit; then
		die "$UNIT exists but wasn't installed by this script; move it aside, or re-run with --no-service"
	fi

	detect_arch
	resolve_version
	TMP="$(mktemp -d)"
	trap 'rm -rf "$TMP"' EXIT
	trap 'exit 1' INT TERM

	UPGRADED=0
	if [ "$(installed_version)" = "$VERSION" ]; then
		info "nylon $VERSION is already installed"
	else
		download_and_install
		UPGRADED=1
	fi

	if [ "$SERVICE" = 1 ]; then
		install_service
	elif [ "$UPGRADED" = 1 ] && managed_unit && service_running; then
		as_root systemctl restart nylon
		info "restarted nylon.service"
	elif [ "$UPGRADED" = 1 ] && [ ! -f "$UNIT" ]; then
		info "next: https://nylon.jq.ax/guides/getting-started"
	fi
}

# run function to ensure partially downloaded script doesn't run
main "$@"
