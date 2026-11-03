#!/usr/bin/env bash
set -e

# ==============================================================================
# Xray Google Script Relay - Automated VPS Server Installer
# Repository: https://github.com/thehornet2002/google_script_relay
# ==============================================================================

RED='\033[0;31m'
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

clear
echo -e "${CYAN}==================================================================${NC}"
echo -e "${CYAN}   ⚡ Xray Google Script Relay - Automated Server Installer       ${NC}"
echo -e "${CYAN}==================================================================${NC}"
echo ""

# 1. Root check
if [ "$(id -u)" -ne 0 ]; then
    echo -e "${RED}[ERROR] This installer must be run as root. Run with sudo: sudo bash $0${NC}"
    exit 1
fi

# 2. Architecture detection
ARCH=$(uname -m)
case "$ARCH" in
    x86_64)
        RELAY_ARCH="amd64"
        ;;
    aarch64|arm64)
        RELAY_ARCH="arm64"
        ;;
    armv7l|armv7|armhf)
        RELAY_ARCH="armv7"
        ;;
    *)
        echo -e "${RED}[ERROR] Unsupported architecture: $ARCH${NC}"
        exit 1
        ;;
esac

echo -e "${GREEN}[+] Detected architecture:${NC} $ARCH (binary: $RELAY_ARCH)"

# 3. Prompt for port
echo ""
echo -e "${YELLOW}Enter the HTTP relay listening port for communication with Google Script:${NC}"
read -rp "Listening Port [Default: 8888]: " INPUT_PORT
PORT=${INPUT_PORT:-8888}

if ! [[ "$PORT" =~ ^[0-9]+$ ]] || [ "$PORT" -lt 1 ] || [ "$PORT" -gt 65535 ]; then
    echo -e "${RED}[ERROR] Invalid port: $PORT. Must be a number between 1 and 65535.${NC}"
    exit 1
fi

echo -e "${GREEN}[+] Configured port:${NC} $PORT"

# 4. Generate random secure tokens and UUID
GEN_TOKEN=$(head -c 32 /dev/urandom | base64 | tr -dc 'a-zA-Z0-9' | head -c 32)
GEN_ADMIN=$(head -c 32 /dev/urandom | base64 | tr -dc 'a-zA-Z0-9' | head -c 32)

if command -v uuidgen &>/dev/null; then
    GEN_UUID=$(uuidgen)
else
    GEN_UUID=$(cat /proc/sys/kernel/random/uuid 2>/dev/null || echo "a0a0a0a0-0000-4000-8000-000000000001")
fi

# 5. Create directories
INSTALL_DIR="/opt/relay"
CONFIG_DIR="$INSTALL_DIR/config"
BIN_DIR="$INSTALL_DIR/bin"

mkdir -p "$CONFIG_DIR" "$BIN_DIR"
chmod 700 "$CONFIG_DIR"

# 6. Stop existing service if running
if systemctl is-active --quiet relay 2>/dev/null; then
    echo -e "${YELLOW}[*] Stopping existing relay service...${NC}"
    systemctl stop relay || true
fi

# 7. Download or use local binary
GITHUB_REPO="thehornet2002/google_script_relay"
TAR_NAME="relay-server-linux-${RELAY_ARCH}.tar.gz"

if [ -f "./relay" ]; then
    echo -e "${GREEN}[+] Using local relay binary...${NC}"
    cp ./relay "$INSTALL_DIR/relay"
elif [ -f "./$TAR_NAME" ]; then
    echo -e "${GREEN}[+] Extracting local archive $TAR_NAME...${NC}"
    tar -xzvf "./$TAR_NAME" -C "$INSTALL_DIR"
else
    echo -e "${BLUE}[*] Fetching latest release from GitHub ($GITHUB_REPO)...${NC}"
    DOWNLOAD_URL=$(curl -s "https://api.github.com/repos/$GITHUB_REPO/releases/latest" | grep "browser_download_url.*$TAR_NAME" | cut -d '"' -f 4 || true)

    if [ -z "$DOWNLOAD_URL" ]; then
        DOWNLOAD_URL="https://github.com/$GITHUB_REPO/releases/download/v1.0.1/$TAR_NAME"
    fi

    echo -e "${BLUE}[*] Downloading from: $DOWNLOAD_URL${NC}"
    curl -sL --retry 3 -o "/tmp/$TAR_NAME" "$DOWNLOAD_URL"
    tar -xzvf "/tmp/$TAR_NAME" -C "$INSTALL_DIR"
    rm -f "/tmp/$TAR_NAME"
fi

chmod +x "$INSTALL_DIR/relay"

# 8. Write configuration file
cat << EOF > "$CONFIG_DIR/server_config.json"
{
  "mode": "server",
  "listen_addr": "0.0.0.0:$PORT",
  "target_addr": "127.0.0.1:10001",
  "token": "$GEN_TOKEN",
  "admin_token": "$GEN_ADMIN",
  "xray_uuid": "$GEN_UUID",
  "server_tuning": {
    "wait_timeout_ms": 800,
    "max_drain_batch_bytes": 131072,
    "downlink_queue_cap": 256
  }
}
EOF

chmod 600 "$CONFIG_DIR/server_config.json"
echo -e "${GREEN}[+] Server configuration generated securely (chmod 600).${NC}"

# 9. Configure systemd service
cat << EOF > /etc/systemd/system/relay.service
[Unit]
Description=Xray Google Apps Script Relay Server
After=network.target

[Service]
Type=simple
User=root
WorkingDirectory=$INSTALL_DIR
ExecStart=$INSTALL_DIR/relay -mode server -config $CONFIG_DIR/server_config.json
Restart=always
RestartSec=3
LimitNOFILE=65535
NoNewPrivileges=true
PrivateTmp=true

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable --now relay

# 10. Configure Firewall (ufw) if active
if command -v ufw &>/dev/null && ufw status | grep -q "Status: active"; then
    echo -e "${GREEN}[+] Opening port $PORT in UFW firewall...${NC}"
    ufw allow "$PORT/tcp" comment "Xray Google Script Relay"
fi

# 11. Fetch public IP
SERVER_IP=$(curl -s --max-time 5 https://api.ipify.org || curl -s --max-time 5 https://icanhazip.com || echo "YOUR_VPS_IP")

# 12. Display summary
sleep 1
clear
echo -e "${GREEN}==================================================================${NC}"
echo -e "${GREEN}      ✅ Relay Server Installed & Started Successfully!           ${NC}"
echo -e "${GREEN}==================================================================${NC}"
echo ""
echo -e "${CYAN}--- Server Connection Details ---${NC}"
echo -e "Server IP:      ${YELLOW}$SERVER_IP${NC}"
echo -e "Listening Port: ${YELLOW}$PORT${NC}"
echo -e "Data Token:     ${GREEN}$GEN_TOKEN${NC}"
echo -e "Admin Token:    ${GREEN}$GEN_ADMIN${NC}"
echo -e "Xray UUID:      ${CYAN}$GEN_UUID${NC}"
echo ""
echo -e "${CYAN}--- 1. In Google Apps Script (gas/Code.gs) ---${NC}"
echo -e "Set line 17 to:"
echo -e "${YELLOW}var VPS_URL = \"http://$SERVER_IP:$PORT/tunnel\";${NC}"
echo -e "*(Or https://your-domain.com/tunnel if using Caddy/Nginx reverse proxy)*"
echo ""
echo -e "${CYAN}--- 2. In Client App (relay-gui.exe) ---${NC}"
echo -e "1. Go to Tab 2 (Google Script Accounts)."
echo -e "2. Auth Token: ${GREEN}$GEN_TOKEN${NC}"
echo -e "3. SNI Front:   ${CYAN}fcm.googleapis.com${NC}"
echo ""
echo -e "${CYAN}--- Service Commands ---${NC}"
echo -e "Status:  ${BLUE}systemctl status relay${NC}"
echo -e "Logs:    ${BLUE}journalctl -u relay -f${NC}"
echo -e "Restart: ${BLUE}systemctl restart relay${NC}"
echo -e "Config:  ${BLUE}$CONFIG_DIR/server_config.json${NC}"
echo -e "${GREEN}==================================================================${NC}"
