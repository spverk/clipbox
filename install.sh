#!/bin/sh
set -eu

ORIG_ARGS="$*"

REPO="${CLIPBOX_REPO:-spverk/clipbox}"
PORT="${CLIPBOX_PORT:-28080}"
DIR="${CLIPBOX_DIR:-/var/lib/clipbox}"
BIND="${CLIPBOX_BIND:-0.0.0.0}"
MAX_SIZE="${CLIPBOX_MAX_SIZE:-2G}"

BIN=/usr/local/bin/clipbox
RUNNER=/usr/local/bin/clipbox-run
CTL=/usr/local/bin/clipboxctl
UNIT=/etc/systemd/system/clipbox.service
OPENRC_SCRIPT=/etc/init.d/clipbox
PIDFILE=/run/clipbox.pid
LOGFILE=/var/log/clipbox.log
SERVICE_USER=clipbox
UNINSTALL=0
MODE=none
IS_CONTAINER=0

usage() {
    cat <<EOF
轻传盒一键安装 / 卸载

用法:
  curl -fsSL https://raw.githubusercontent.com/$REPO/main/install.sh | sh -s -- [参数]

已经是 root（提示符为 #）时直接执行；普通用户请在前面加 sudo。

参数:
  --port <端口>       监听端口，默认 $PORT
  --dir <目录>        数据目录，默认 $DIR
  --bind <地址>       监听地址，默认 $BIND
  --max-size <大小>   单文件上限，默认 $MAX_SIZE（支持 K/M/G）
  --repo <owner/repo> 自定义 GitHub 仓库，默认 $REPO
  --uninstall         停止服务并删除程序（保留数据目录）
  -h, --help          显示帮助

支持 systemd、OpenRC 以及没有 init 的容器（Podman / Docker / LXC）。
容器内会自动后台启动，并生成 clipboxctl {start|stop|restart|status}。
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
    echo "已经是 root 时去掉 sudo。" >&2
    exit 1
fi

if [ -f /run/.containerenv ] || [ -f /.dockerenv ]; then
    IS_CONTAINER=1
fi

if [ -d /run/systemd/system ] && command -v systemctl >/dev/null 2>&1; then
    MODE=systemd
elif command -v rc-service >/dev/null 2>&1 && command -v rc-update >/dev/null 2>&1 && [ -d /run/openrc ]; then
    MODE=openrc
fi

if [ "$UNINSTALL" -eq 1 ]; then
    echo "==> 停止并卸载轻传盒（环境: $MODE）"
    case "$MODE" in
        systemd)
            systemctl disable --now clipbox >/dev/null 2>&1 || true
            rm -f "$UNIT"
            systemctl daemon-reload >/dev/null 2>&1 || true
            ;;
        openrc)
            rc-service clipbox stop >/dev/null 2>&1 || true
            rc-update del clipbox default >/dev/null 2>&1 || true
            rm -f "$OPENRC_SCRIPT"
            ;;
        none)
            if [ -x "$CTL" ]; then
                "$CTL" stop >/dev/null 2>&1 || true
            fi
            ;;
    esac
    rm -f "$BIN" "$RUNNER" "$CTL" "$PIDFILE" "$LOGFILE"
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
[ -x "$NOLOGIN" ] || NOLOGIN=/sbin/nologin
[ -x "$NOLOGIN" ] || NOLOGIN=/bin/false

if [ "$MODE" != none ]; then
    if ! id "$SERVICE_USER" >/dev/null 2>&1; then
        echo "==> 创建系统用户 $SERVICE_USER"
        if command -v useradd >/dev/null 2>&1; then
            useradd -r -d "$DIR" -s "$NOLOGIN" "$SERVICE_USER" 2>/dev/null \
                || useradd --system --home "$DIR" --shell "$NOLOGIN" "$SERVICE_USER" 2>/dev/null \
                || true
        else
            adduser -S -D -H -h "$DIR" -s "$NOLOGIN" "$SERVICE_USER" 2>/dev/null \
                || adduser --system --home "$DIR" --shell "$NOLOGIN" "$SERVICE_USER" 2>/dev/null \
                || true
        fi
        if ! id "$SERVICE_USER" >/dev/null 2>&1; then
            echo "创建用户 $SERVICE_USER 失败。" >&2
            exit 1
        fi
    fi
    # 不能假设存在同名组：shadow 的 useradd -r 会建同名组，但 busybox
    # adduser -S 创建的系统用户主组是 nogroup(65533)，/etc/group 里没有
    # clipbox 组。硬编码 clipbox:clipbox 会让 chown / command_user 报
    # "unknown user/group"。一律改用真实数值 UID:GID，跨平台都成立。
    SVC_UID="$(id -u "$SERVICE_USER")"
    SVC_GID="$(id -g "$SERVICE_USER")"
fi

echo "==> 准备数据目录 $DIR"
mkdir -p "$DIR"
if [ "$MODE" != none ]; then
    chown -R "$SVC_UID:$SVC_GID" "$DIR"
fi
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
cp "$TMP" "$BIN"
chmod 0755 "$BIN"

echo "==> 写入启动脚本 $RUNNER"
cat > "$RUNNER" <<EOF
#!/bin/sh
umask 0077
export GOMEMLIMIT=16MiB
export GOGC=50
exec "$BIN" --bind "$BIND" --port "$PORT" --dir "$DIR" --max-size "$MAX_SIZE"
EOF
chmod 0755 "$RUNNER"

case "$MODE" in
    systemd)
        echo "==> 写入 systemd 服务"
        cat > "$UNIT" <<EOF
[Unit]
Description=轻传盒 lightweight file and text clipboard
After=network.target

[Service]
Type=simple
User=$SERVICE_USER
ExecStart=$RUNNER
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
        i=0
        while [ "$i" -lt 5 ]; do
            if systemctl is-active --quiet clipbox; then
                break
            fi
            i=$((i + 1))
            sleep 1
        done
        if ! systemctl is-active --quiet clipbox; then
            echo "服务启动失败，最近日志：" >&2
            journalctl -u clipbox -n 30 --no-pager >&2 || true
            exit 1
        fi
        ;;
    openrc)
        echo "==> 写入 OpenRC 服务"
        : >> "$LOGFILE"
        chown "$SVC_UID:$SVC_GID" "$LOGFILE" 2>/dev/null || true
        chmod 0600 "$LOGFILE" 2>/dev/null || true

        # 有 supervise-daemon 就加进程守护：clipbox 崩溃后自动拉起，
        # 避免服务 crashed 后轻传盒域名一直回源失败(520)。
        SUP_LINES=""
        if command -v supervise-daemon >/dev/null 2>&1; then
            SUP_LINES="supervisor=supervise-daemon
respawn_delay=3
respawn_max=100"
        fi

        cat > "$OPENRC_SCRIPT" <<EOF
#!/sbin/openrc-run

name="clipbox"
description="轻传盒 lightweight file and text clipboard"
command="$RUNNER"
command_background=true
directory="$DIR"
pidfile="$PIDFILE"
command_user="$SVC_UID:$SVC_GID"
$SUP_LINES
output_log="$LOGFILE"
error_log="$LOGFILE"

depend() {
    need net
}
EOF
        chmod 0755 "$OPENRC_SCRIPT"

        rc-update add clipbox default >/dev/null 2>&1 || true
        rc-service clipbox restart

        echo "==> 等待服务启动"
        sleep 1
        if ! rc-service clipbox status >/dev/null 2>&1; then
            echo "服务启动失败，最近日志：" >&2
            tail -n 30 "$LOGFILE" >&2 || true
            exit 1
        fi
        ;;
    none)
        echo "==> 写入后台控制脚本 $CTL"
        : >> "$LOGFILE"
        chmod 0600 "$LOGFILE" 2>/dev/null || true

        cat > "$CTL" <<EOF
#!/bin/sh
set -eu

PIDFILE="$PIDFILE"
LOGFILE="$LOGFILE"
RUNNER="$RUNNER"

running() {
    [ -s "\$PIDFILE" ] || return 1
    pid="\$(cat "\$PIDFILE" 2>/dev/null || true)"
    [ -n "\$pid" ] || return 1
    kill -0 "\$pid" 2>/dev/null
}

start() {
    if running; then
        echo "clipbox 已在运行 (PID \$(cat "\$PIDFILE"))"
        return 0
    fi
    : >> "\$LOGFILE"
    nohup "\$RUNNER" >> "\$LOGFILE" 2>&1 &
    echo \$! > "\$PIDFILE"
    sleep 1
    if running; then
        echo "clipbox 已启动 (PID \$(cat "\$PIDFILE"))"
    else
        echo "clipbox 启动失败，最近日志:" >&2
        tail -n 20 "\$LOGFILE" >&2 || true
        rm -f "\$PIDFILE"
        return 1
    fi
}

stop() {
    if ! running; then
        rm -f "\$PIDFILE"
        echo "clipbox 未运行"
        return 0
    fi
    pid="\$(cat "\$PIDFILE")"
    kill "\$pid" 2>/dev/null || true
    i=0
    while kill -0 "\$pid" 2>/dev/null && [ "\$i" -lt 5 ]; do
        i=\$((i + 1))
        sleep 1
    done
    kill -9 "\$pid" 2>/dev/null || true
    rm -f "\$PIDFILE"
    echo "clipbox 已停止"
}

status() {
    if running; then
        echo "clipbox 运行中 (PID \$(cat "\$PIDFILE"))"
    else
        echo "clipbox 未运行"
        return 1
    fi
}

case "\${1:-}" in
    start)
        start
        ;;
    stop)
        stop
        ;;
    restart)
        stop
        start
        ;;
    status)
        status
        ;;
    *)
        echo "用法: clipboxctl {start|stop|restart|status}" >&2
        exit 2
        ;;
esac
EOF
        chmod 0755 "$CTL"

        if ! "$CTL" start; then
            exit 1
        fi
        ;;
esac

if command -v ufw >/dev/null 2>&1 && ufw status 2>/dev/null | grep -q "Status: active"; then
    ufw allow "$PORT"/tcp >/dev/null 2>&1 || true
    echo "==> 已尝试在 ufw 放行 $PORT/tcp"
fi

if command -v firewall-cmd >/dev/null 2>&1 && firewall-cmd --state >/dev/null 2>&1; then
    firewall-cmd --permanent --add-port="$PORT"/tcp >/dev/null 2>&1 || true
    firewall-cmd --reload >/dev/null 2>&1 || true
    echo "==> 已尝试在 firewalld 放行 $PORT/tcp"
fi

CHECK_URL="http://127.0.0.1:$PORT/api/health"
case "$BIND" in
    0.0.0.0|127.0.0.1|localhost) ;;
    *) CHECK_URL="" ;;
esac

if [ -n "$CHECK_URL" ]; then
    echo "==> 健康检查"
    i=0
    ok=0
    while [ "$i" -lt 5 ]; do
        if curl -fsS --max-time 2 "$CHECK_URL" >/dev/null 2>&1; then
            ok=1
            break
        fi
        i=$((i + 1))
        sleep 1
    done
    if [ "$ok" -ne 1 ]; then
        echo "健康检查失败: $CHECK_URL" >&2
        if [ "$MODE" = systemd ]; then
            journalctl -u clipbox -n 30 --no-pager >&2 || true
        else
            tail -n 30 "$LOGFILE" >&2 || true
        fi
        exit 1
    fi
fi

IP="$(curl -fsSL --max-time 5 https://api.ipify.org 2>/dev/null || true)"
[ -n "$IP" ] || IP="$(curl -fsSL --max-time 5 https://ifconfig.me 2>/dev/null || true)"
[ -n "$IP" ] || IP="你的服务器IP"

case "$MODE" in
    systemd)
        cat <<EOF

轻传盒已安装并开机自启（systemd）。

  网页:      http://$IP:$PORT
  上传:      curl -T 本地文件 http://$IP:$PORT/api/upload
  下载:      curl -fsSL http://$IP:$PORT/api/download -o 文件名
  数据目录:  $DIR
  查看状态:  systemctl status clipbox
  实时日志:  journalctl -u clipbox -f
  卸载:      sh install.sh --uninstall
EOF
        ;;
    openrc)
        cat <<EOF

轻传盒已安装并加入 OpenRC 默认运行级别。

  网页:      http://$IP:$PORT
  上传:      curl -T 本地文件 http://$IP:$PORT/api/upload
  下载:      curl -fsSL http://$IP:$PORT/api/download -o 文件名
  数据目录:  $DIR
  查看状态:  rc-service clipbox status
  实时日志:  tail -f $LOGFILE
  卸载:      sh install.sh --uninstall
EOF
        ;;
    none)
        cat <<EOF

轻传盒已安装并在后台启动。

  网页:      http://$IP:$PORT
  上传:      curl -T 本地文件 http://$IP:$PORT/api/upload
  下载:      curl -fsSL http://$IP:$PORT/api/download -o 文件名
  数据目录:  $DIR
  查看状态:  clipboxctl status
  启动/停止: clipboxctl start | clipboxctl stop
  实时日志:  tail -f $LOGFILE
  卸载:      sh install.sh --uninstall
EOF
        if [ "$IS_CONTAINER" -eq 1 ]; then
            cat <<EOF

注意：检测到容器环境（没有 systemd / OpenRC），容器内无法真正开机自启。
容器每次重启后需要重新执行：clipboxctl start
如果是 Podman / Docker，请把这条命令写进容器的启动命令或 entrypoint；
宿主机的 --restart=always 只会重启容器，不会自动拉起容器里的 clipbox。
EOF
        else
            cat <<EOF

注意：当前系统没有 systemd / OpenRC，已后台启动，但没有开机自启。
机器重启后需要重新执行：clipboxctl start
可以把这条命令写进 /etc/rc.local 或自己的启动脚本。
EOF
        fi
        ;;
esac

cat <<EOF

注意：服务无登录鉴权，请只在自己可信的网络里使用，或额外加反向代理与访问控制。
EOF
