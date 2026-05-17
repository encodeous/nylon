#!/bin/bash
# Self-contained demo recorder for nylon failover.
# Produces demo/output/demo.gif — the only dependency is Docker + python3.
#
# Layout (tmux):
#   ┌─────────────────────┬──────────────────────┐
#   │  mtr (traceroute)   │  actions + topology  │
#   ├─────────────────────┤                      │
#   │  ping               │                      │
#   └─────────────────────┴──────────────────────┘
set -euo pipefail

DEMO_DIR="$(cd "$(dirname "$0")" && pwd)"
OUTPUT_DIR="$DEMO_DIR/output"
rm -rf "$OUTPUT_DIR"
mkdir -p "$OUTPUT_DIR"

# ── Helpers ──────────────────────────────────────────────────────────
info() { printf "\033[1;36m▸ %s\033[0m\n" "$*"; }

dc() { docker compose -f "$DEMO_DIR/docker-compose.yml" -p nylon-demo "$@"; }

exec_alice() { docker exec nylon-demo-alice "$@"; }

cleanup() {
  info "Cleaning up containers..."
  dc down --remove-orphans 2>/dev/null || true
}
trap cleanup EXIT

# ── Step 0: Build image and generate configs ────────────────────────
info "Building nylon image..."
dc build --quiet 2>/dev/null

info "Generating keys..."
CONFIGS="$DEMO_DIR/configs"
rm -rf "$CONFIGS"
mkdir -p "$CONFIGS"

IMAGE=$(dc config --images | head -1)

gen_key() {
  docker run --rm --entrypoint /usr/local/bin/nylon "$IMAGE" key 2>/tmp/nylon_pub
  cat /tmp/nylon_pub
}

alice_raw=$(gen_key)
alice_priv=$(echo "$alice_raw" | sed -n '1p')
alice_pub=$(echo "$alice_raw" | sed -n '2p')

bob_raw=$(gen_key)
bob_priv=$(echo "$bob_raw" | sed -n '1p')
bob_pub=$(echo "$bob_raw" | sed -n '2p')

charlie_raw=$(gen_key)
charlie_priv=$(echo "$charlie_raw" | sed -n '1p')
charlie_pub=$(echo "$charlie_raw" | sed -n '2p')

cat > "$CONFIGS/central.yaml" << EOF
routers:
  - id: alice
    pubkey: ${alice_pub}
    addresses: [10.99.0.1]
    endpoints:
      - "172.31.0.10:57175"
  - id: bob
    pubkey: ${bob_pub}
    addresses: [10.99.0.2]
    endpoints:
      - "172.31.0.11:57175"
  - id: charlie
    pubkey: ${charlie_pub}
    addresses: [10.99.0.3]
    endpoints:
      - "172.31.0.12:57175"

graph:
  - alice, bob, charlie
  - alice, charlie
EOF

for node in alice bob charlie; do
  eval priv=\$${node}_priv
  cat > "$CONFIGS/${node}.yaml" << EOF
id: ${node}
key: ${priv}
port: 57175
interface_name: nylon0
EOF
done

info "Configs generated."

# ── Step 1: Start the mesh ─────────────────────────────────────────
info "Starting 3-node mesh..."
dc up -d

# Don't wait for convergence here — we record from the start

# ── Step 2: Create the demo scripts inside alice ───────────────────

# Top-left pane: mtr (no display mode header)
exec_alice bash -c 'cat > /tmp/mtr.sh << '\''SCRIPT'\''
#!/bin/bash
while ! ping -c 1 -W 1 10.99.0.3 >/dev/null 2>&1; do sleep 0.5; done
exec mtr --displaymode 0 -i 0.5 --no-dns -a 10.99.0.1 10.99.0.3
SCRIPT
chmod +x /tmp/mtr.sh'

# Bottom-left pane: ping
exec_alice bash -c 'cat > /tmp/ping.sh << '\''SCRIPT'\''
#!/bin/bash
while ! ping -c 1 -W 1 10.99.0.3 >/dev/null 2>&1; do sleep 0.5; done
echo "── ping alice (10.99.0.1) → charlie (10.99.0.3) ──"
echo ""
exec fping -l -e -q -p 1000 -t 10 10.99.0.3
SCRIPT
chmod +x /tmp/ping.sh'

# Right pane: orchestrates the demo
exec_alice bash -c 'cat > /tmp/right.sh << '\''SCRIPT'\''
#!/bin/bash

# ── Color helpers ──
R="\033[0m"       # reset
BOLD="\033[1m"
RED="\033[31m"
GREEN="\033[32m"
YELLOW="\033[33m"
CYAN="\033[36m"
DIM="\033[90m"
BRED="\033[1;31m"
BGREEN="\033[1;32m"
BYELLOW="\033[1;33m"
BCYAN="\033[1;36m"

# Helpers
START=$(date +%s)
elapsed() { echo "$(( $(date +%s) - START ))s"; }
header()  { echo -e "${BYELLOW}── $* ──${R}"; }
ok()      { echo -e "${BGREEN}  [$(elapsed)] ✓ $*${R}"; }
info()    { echo -e "${DIM}  $*${R}"; }
fail()    { echo -e "${BRED}  [$(elapsed)] ✗ $*${R}"; }

# ── Topology diagrams ──
topo_normal() {
  echo -e "  ${BOLD}alice${R} .1 ────── ${BOLD}charlie${R} .3"
  echo -e "  ${BOLD}${R}       |           |"
  echo -e "  ${BOLD}bob${R}   .2 ──────────┘"
}

topo_broken() {
  echo -e "  ${BOLD}alice${R} .1 ${RED}──✗───${R} ${BOLD}charlie${R} .3"
  echo -e "  ${BOLD}${R}       |           |"
  echo -e "  ${BOLD}bob${R}   .2 ──────────┘"
}

topo_rerouted() {
  echo -e "  ${BOLD}alice${R} .1 ${RED}──✗───${R} ${BOLD}charlie${R} .3"
  echo -e "  ${BOLD}${R}       ${GREEN}|${R}           ${GREEN}|${R}"
  echo -e "  ${GREEN}${BOLD}bob${R}   .2 ${GREEN}──────────┘${R}"
}

topo_restored() {
  echo -e "  ${BOLD}alice${R} .1 ${GREEN}──────${R} ${BOLD}charlie${R} .3"
  echo -e "  ${BOLD}${R}       |           |"
  echo -e "  ${BOLD}bob${R}   .2 ──────────┘"
}

# ── Demo script ──
echo -e "${BCYAN}# nylon — self-healing WireGuard mesh${R}"
echo ""
header "topology"
echo ""
topo_normal
echo ""

echo -ne "${DIM}  [$(elapsed)] waiting for mesh convergence"

while ! ping -c 1 -W 1 10.99.0.3 >/dev/null 2>&1; do
  echo -ne "."
  sleep 0.5
done
echo -e "${R}"

ok "mesh converged"
info "route: alice → charlie (direct, 1 hop)"
echo ""

sleep 5

header "cutting direct link"
echo ""
topo_broken
echo ""
echo "  $ iptables -A INPUT -s 172.31.0.12 -j DROP"
iptables -A INPUT -s 172.31.0.12 -j DROP
echo "  $ iptables -A OUTPUT -d 172.31.0.12 -j DROP"
iptables -A OUTPUT -d 172.31.0.12 -j DROP
echo ""

echo -ne "${DIM}  [$(elapsed)] waiting for reroute"
while true; do
  if ping -c 1 -W 1 10.99.0.3 >/dev/null 2>&1; then
    break
  fi
  echo -ne "."
  sleep 0.5
done
echo -e "${R}"

ok "rerouted through bob!"
info "route: alice → bob → charlie (2 hops)"
echo ""
topo_rerouted
echo ""

sleep 5

header "restoring direct link"
echo ""
echo "  $ iptables -F"
iptables -F
echo ""

echo -ne "${DIM}  [$(elapsed)] waiting for direct route"
for i in $(seq 1 30); do
  result=$(ping -c 1 -W 1 10.99.0.3 2>/dev/null | grep "ttl=64" || true)
  if [ -n "$result" ]; then
    break
  fi
  echo -ne "."
  sleep 0.5
done
echo -e "${R}"

ok "direct link restored"
info "route: alice → charlie (direct, 1 hop)"
echo ""
topo_restored
echo ""

echo -e "${BCYAN}  Zero config changes. Routes healed automatically.${R}"
echo -e "${BCYAN}  https://github.com/encodeous/nylon${R}"
echo ""

for i in $(seq 10 -1 1); do
  echo -ne "\r${DIM}  exiting in ${i}s... ${R}"
  sleep 1
done
echo ""

tmux kill-session -t demo
SCRIPT
chmod +x /tmp/right.sh'

# Bottom-right pane: IP legend
exec_alice bash -c 'cat > /tmp/legend.sh << '\''SCRIPT'\''
#!/bin/bash
echo ""
echo -e "\033[1;33m  nodes\033[0m"
echo ""
echo -e "  \033[1malice\033[0m    10.99.0.1  (172.31.0.10)"
echo -e "  \033[1mbob\033[0m      10.99.0.2  (172.31.0.11)"
echo -e "  \033[1mcharlie\033[0m  10.99.0.3  (172.31.0.12)"
echo ""
echo -e "\033[90m  tunnel ip    (docker ip)\033[0m"
sleep infinity
SCRIPT
chmod +x /tmp/legend.sh'

# ── Step 3: Record with asciinema + tmux ───────────────────────────
info "Recording demo..."

# Layout:
# ┌──────────────────┬─────────────┐
# │  mtr (top-left)  │ actions     │
# ├──────────────────┤ (top-right) │
# │  ping (bot-left) ├─────────────┤
# │                  │ legend      │
# └──────────────────┴─────────────┘
docker exec -e TERM=xterm-256color nylon-demo-alice bash -c '
asciinema rec /tmp/demo.cast --cols 140 --rows 35 --overwrite -c "
  tmux new-session -d -s demo -x 140 -y 35 /tmp/mtr.sh

  # 1. Split horizontally to create the right pane (becomes index 0.1)
  tmux split-window -h -t demo:0.0 -l 55 /tmp/right.sh

  # 2. Split the right pane (0.1) vertically for the legend
  tmux split-window -v -t demo:0.1 -l 9 /tmp/legend.sh

  # 3. Split the left pane (0.0) vertically for ping
  tmux split-window -v -t demo:0.0 -l 20 /tmp/ping.sh

  exec tmux attach -t demo
"'

info "Recording complete."

# ── Step 4: Extract the cast file ──────────────────────────────────
docker cp nylon-demo-alice:/tmp/demo.cast "$OUTPUT_DIR/demo.cast"

if [ ! -f "$OUTPUT_DIR/demo.cast" ]; then
  echo "Error: cast file was not created."
  exit 1
fi

info "Cast file saved to $OUTPUT_DIR/demo.cast"

# ── Step 5: Convert to GIF ─────────────────────────────────────────
info "Converting to GIF..."
docker run --rm \
  -v "$OUTPUT_DIR:/data" \
  ghcr.io/asciinema/agg:latest \
  /data/demo.cast /data/demo.gif \
  --font-size 14 \
  --idle-time-limit 2.0 \
  --speed 1.0

if [ -f "$OUTPUT_DIR/demo.gif" ]; then
  info "Done! GIF saved to: $OUTPUT_DIR/demo.gif"
  ls -lh "$OUTPUT_DIR/demo.gif"
else
  echo "Error: GIF was not created. Cast file: $OUTPUT_DIR/demo.cast"
  exit 1
fi
