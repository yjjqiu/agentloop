#!/usr/bin/env bash
# AgentDeck daemon 一键安装（Linux + systemd）
#
#   sudo ./install.sh
#
# 可用环境变量覆盖默认值：
#   INSTALL_DIR=/opt/agentdeck  DATA_DIR=/var/lib/agentdeck  ETC_DIR=/etc/agentdeck
#   SERVICE_USER=agentdeck      ADDR=0.0.0.0:3210
set -euo pipefail

BIN=agentdeckd
INSTALL_DIR=${INSTALL_DIR:-/opt/agentdeck}
DATA_DIR=${DATA_DIR:-/var/lib/agentdeck}
ETC_DIR=${ETC_DIR:-/etc/agentdeck}
SERVICE_USER=${SERVICE_USER:-agentdeck}
ADDR=${ADDR:-0.0.0.0:3210}
UNIT=/etc/systemd/system/agentdeckd.service

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
src=
for candidate in "$here/$BIN" "$here/../$BIN" "$here/../deploy/$BIN"; do
  if [[ -f $candidate ]]; then src=$candidate; break; fi
done
if [[ -z $src ]]; then
  echo "✗ 没找到 $BIN 二进制：请把它放在解压目录根下再运行本脚本。" >&2
  exit 1
fi

if [[ $EUID -ne 0 ]]; then
  echo "✗ 请用 root 运行：sudo $0" >&2
  exit 1
fi

gen_token() {
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -hex 24
  else
    od -An -N24 -tx1 /dev/urandom | tr -d ' \n'
  fi
}

echo "• 规划：二进制 $INSTALL_DIR/$BIN，数据 $DATA_DIR，配置 $ETC_DIR"

if ! id -u "$SERVICE_USER" >/dev/null 2>&1; then
  useradd --system --create-home --shell /bin/bash "$SERVICE_USER"
  echo "• 已创建服务用户 $SERVICE_USER"
fi

install -d -m 0755 "$INSTALL_DIR" "$ETC_DIR"
install -d -o "$SERVICE_USER" -g "$SERVICE_USER" -m 0750 "$DATA_DIR"
install -m 0755 "$src" "$INSTALL_DIR/$BIN"

token=
if [[ -f $ETC_DIR/agentdeck.env ]]; then
  echo "• 沿用已有配置 $ETC_DIR/agentdeck.env"
  token=$(sed -n 's/^AGENTDECK_TOKEN=//p' "$ETC_DIR/agentdeck.env" | head -n1)
else
  token=$(gen_token)
  cat > "$ETC_DIR/agentdeck.env" <<EOF
AGENTDECK_TOKEN=$token
AGENTDECK_REPO=${AGENTDECK_REPO:-yjjqiu/agentloop}
PATH=$INSTALL_DIR/npm-global/bin:/usr/local/bin:/usr/bin:/bin
EOF
  chmod 0600 "$ETC_DIR/agentdeck.env"
  echo "• 已生成访问令牌并写入 $ETC_DIR/agentdeck.env（权限 0600）"
fi

cat > "$UNIT" <<EOF
[Unit]
Description=AgentDeck daemon（多 Agent 工作台，浏览器访问）
Documentation=https://github.com/yjjqiu/agentloop
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=$SERVICE_USER
Group=$SERVICE_USER
WorkingDirectory=$INSTALL_DIR
EnvironmentFile=-$ETC_DIR/agentdeck.env
ExecStart=$INSTALL_DIR/$BIN -addr $ADDR -data $DATA_DIR
Restart=always
RestartSec=3
LimitNOFILE=65536
KillMode=mixed
TimeoutStopSec=20
NoNewPrivileges=true
PrivateTmp=true

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable --now agentdeckd.service
sleep 1

if systemctl is-active --quiet agentdeckd.service; then
  port=${ADDR##*:}
  echo
  echo "✅ AgentDeck 已启动： http://<服务器地址>:$port"
  echo "   访问令牌： ${token:-（见 $ETC_DIR/agentdeck.env）}"
  echo
  echo "下一步（按需）："
  echo "  1) 给服务用户装 CLI，例如："
  echo "     sudo -u $SERVICE_USER env PATH=$INSTALL_DIR/npm-global/bin:\$PATH npm i -g @anthropic-ai/claude-code"
  echo "  2) 建议前面加 Nginx + HTTPS（见 deploy/nginx-agentdeck.conf）"
  echo "  3) 查看日志： journalctl -u agentdeckd -f"
else
  echo "✗ 服务没起来，查看日志： journalctl -u agentdeckd -n 50 --no-pager" >&2
  exit 1
fi