#!/bin/sh
set -eu

ORIG_ARGS="$*"

REPO="${CLIPBOX_REPO:-spverk/clipbox}"
PORT="${CLIPBOX_PORT:-28080}"
DIR="${CLIPBOX_DIR:-/var/lib/clipbox}"
BIND="${CLIPBOX_BIND:-0.0.0.0}"
MAX_SIZE="${CLIPBOX_MAX_SIZE:-2G}"

BIN=/usr/local/bin/clipbox
UNIT=/etc/systemd/system/clipbox.service
SERVICE_USER=clipbox
UNINSTALL=0

usage() {
    cat <<EOF
轻传盒一键安装 / 卸载

用法:
  curl -fsSL https://raw.githubusercontent.com/$REPO/main/install.sh | sudo sh -s -- [参数]

参数:
  --port <端口>       监听端口，默认 $PORT
  --dir <目录>        数据目录，默认 $DIR
  --bind <地址>       监听地址，默认 $BIND
  --max-size <大小>   单文件上限，默认 $MAX_SIZE（支持 K/M/G）
  --repo <owner/repo> 自定义 GitHub 仓库，默认 $REPO
  --uninstall         停止服务并删除程序（保留数据目录）
  -h, --help          显示帮助
EOF
}

need_value() {
    if [ $# -lt 2 ]; then
        echo "参数 $1 缺少值" >&2
        exit 2
    fi
}

while [ $# -gt 0 ]; do
    case "$1" in
        --port)
            need_value "$@"
            PORT=$2
            shift 2
            ;;
        --dir)
            need_value "$@"
            DIR=$2
            shift 2
            ;;
        --bind)
            need_value "$@"
            BIND=$2
            shift 2
            ;;
        --max-size)
            need_value "$@"
            MAX_SIZE=$2
            shift 2
            ;;
        --repo)
            need_value "$@"
            REPO=$2
            shift 2
            ;;
        --uninstall)
            UNINSTALL=1
            shift
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            echo "未知参数: $1" >&2
            usage >&2
            exit 2
            ;;
    esac
done

if [ "$(id -u)" -ne 0 ]; then
    echo "需要 root 权限。请用：" >&2
    echo "  curl -fsSL https://raw.githubusercontent.com/$REPO/main/install.sh | sudo sh -s -- $ORIG_ARGS" >&2
    exit 1
fi

if [ ! -d /run/systemd/system ] && ! command -v systemctl >/dev/null 2>&1; then
    echo "没有检测到 systemd，无法配置开机自启。" >&2
    echo "可以手动运行: clipbox --bind $BIND --port $PORT --dir $DIR --max-size $MAX_SIZE" >&2
    exit 1
fi

if [ "$UNINSTALL" -eq 1 ]; then
    echo "==> 停止并卸载轻传盒"
    systemctl disable --now clipbox >/dev/null 2>&1 || true
    rm -f "$UNIT"
    systemctl daemon-reload >/dev/null 2>&1 || true
    rm -f "$BIN"
    echo "已卸载。数据目录保留在: $DIR"
    echo "如果不需要数据，可手动删除: rm -rf $DIR"
    exit 0
fi

case "$(uname -m)" in
    x86_64|amd64) ARCH=amd64 ;;
    aarch64|arm64) ARCH=arm64 ;;
    *)
        echo "不支持的 CPU 架构: $(uname -m)" >&2
        exit 1
        ;;
esac

if ! command -v curl >/dev/null 2>&1; then
    echo "需要 curl，请先安装。" >&2
    exit 1
fi

NOLOGIN=/usr/sbin/nologin
[ -x "$NOLOGIN" ] || NOLOGIN=/bin/false

if ! id "$SERVICE_USER" >/dev/null 2>&1; then
    echo "==> 创建系统用户 $SERVICE_USER"
    if command -v useradd >/dev/null 2>&1; then
        useradd --system --home "$DIR" --shell "$NOLOGIN" "$SERVICE_USER"
    else
        adduser --system --home "$DIR" --shell "$NOLOGIN" "$SERVICE_USER"
    fi
fi

echo "==> 准备数据目录 $DIR"
mkdir -p "$DIR"
chown -R "$SERVICE_USER:$SERVICE_USER" "$DIR"
chmod 0750 "$DIR"

echo "==> 下载 clipbox ($ARCH)"
URL="https://github.com/$REPO/releases/latest/download/clipbox-linux-$ARCH"
TMP="$(mktemp)"
trap 'rm -f "$TMP"' EXIT
if ! curl -fL --retry 2 --connect-timeout 10 -o "$TMP" "$URL"; then
    echo "下载失败: $URL" >&2
    echo "请确认仓库已发布 Release，或检查网络。" >&2
    exit 1
fi
install -m 0755 "$TMP" "$BIN"

echo "==> 写入 systemd 服务"
cat > "$UNIT" <<EOF
[Unit]
Description=轻传盒 lightweight file and text clipboard
After=network.target

[Service]
Type=simple
User=$SERVICE_USER
Group=$SERVICE_USER
ExecStart=$BIN --bind $BIND --port $PORT --dir $DIR --max-size $MAX_SIZE
WorkingDirectory=$DIR
Environment=GOMEMLIMIT=16MiB
Environment=GOGC=50
Restart=always
RestartSec=3
UMask=0077
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=$DIR
MemoryHigh=20M
MemoryMax=28M
LimitNOFILE=8192

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable clipbox >/dev/null 2>&1 || true
systemctl restart clipbox

echo "==> 等待服务启动"
sleep 1
if ! systemctl is-active --quiet clipbox; then
    echo "服务启动失败，最近日志：" >&2
    journalctl -u clipbox -n 30 --no-pager >&2 || true
    exit 1
fi

if command -v ufw >/dev/null 2>&1 && ufw status 2>/dev/null | grep -q "Status: active"; then
    ufw allow "$PORT"/tcp >/dev/null 2>&1 || true
    echo "==> 已尝试在 ufw 放行 $PORT/tcp"
fi

if command -v firewall-cmd >/dev/null 2>&1 && firewall-cmd --state >/dev/null 2>&1; then
    firewall-cmd --permanent --add-port="$PORT"/tcp >/dev/null 2>&1 || true
    firewall-cmd --reload >/dev/null 2>&1 || true
    echo "==> 已尝试在 firewalld 放行 $PORT/tcp"
fi

IP="$(curl -fsSL --max-time 5 https://api.ipify.org 2>/dev/null || true)"
[ -n "$IP" ] || IP="$(curl -fsSL --max-time 5 https://ifconfig.me 2>/dev/null || true)"
[ -n "$IP" ] || IP="你的服务器IP"

cat <<EOF

轻传盒已安装并开机自启。

  网页:      http://$IP:$PORT
  上传:      curl -T 本地文件 http://$IP:$PORT/api/upload
  下载:      curl -fsSL http://$IP:$PORT/api/download -o 文件名
  数据目录:  $DIR
  查看状态:  systemctl status clipbox
  实时日志:  journalctl -u clipbox -f
  卸载:      sh install.sh --uninstall

注意：服务无登录鉴权，请只在自己可信的网络里使用，或额外加反向代理与访问控制。
EOF
