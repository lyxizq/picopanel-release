#!/bin/sh
# lz-server 启动/停止脚本（服务端）
#
# 部署：本脚本与 lz-server 二进制放在同一目录（推荐 /home/lz-monitor/server/，
#       与 agent 目录 /home/lz-monitor/agent/ 分开），可在任意位置调用，
#       路径一律相对脚本所在目录。
# 用法：./lz-server.sh {start|stop|restart|status|upgrade} [额外参数]
#       额外参数原样透传给二进制，例如忘记登录名/密码时重置管理员：
#       ./lz-server.sh start -resetuser   （重置为 admin/admin123 后正常启动）
#       ./lz-server.sh start -resettotp   （清除两步验证配置后正常启动，密码可直接登录）
#       二进制以 gzip 压缩包分发（同目录 lz-server.gz，make release 产物自带）：
#       start 时无二进制会自动解压；upgrade [压缩包] 解压替换并重启——
#       rsync 整目录（或单独上传 .gz）后直接执行：
#       ./lz-server.sh upgrade   （旧版保留为 lz-server.old，回滚: mv 回去后 restart）
#
# 日志：程序自身写入 <本目录>/lz-server.log，默认总占用 300MB 自动轮转
#       （当前文件 + .1/.2 两个备份），因此 nohup 输出直接丢弃（>/dev/null）。
#       需要自定义上限时在同目录 server.yaml 里调 log_max_mb。
set -eu

DIR=$(cd "$(dirname "$0")" && pwd)
BIN=$DIR/lz-server
PKG=$DIR/lz-server.gz   # 分发形态：gzip 压缩包（make release 产物自带）
PIDFILE=$DIR/lz-server.pid
CONF=$DIR/server.yaml   # 可选；存在则自动加载

# Go 1.25+ 默认按 cgroup CPU 配额推导 GOMAXPROCS；部分虚拟化环境（OpenVZ/LXC 等）
# 配额异常会被推导为 0，启动即崩（fatal error: procresize: invalid arg）。
# 未显式设置时回退到逻辑核数，规避该问题。
if [ -z "${GOMAXPROCS:-}" ]; then
    NP=$(nproc 2>/dev/null || echo 2)
    [ "$NP" -ge 1 ] 2>/dev/null || NP=2
    export GOMAXPROCS=$NP
fi

pid_alive() {
    [ -f "$PIDFILE" ] && kill -0 "$(cat "$PIDFILE")" 2>/dev/null
}

# do_start 透传 start/restart 之后的额外参数给二进制（如 -resetuser）
do_start() {
    if pid_alive; then
        echo "lz-server 已在运行 (pid $(cat "$PIDFILE"))"
        return 0
    fi
    # 首次部署：目录里只有压缩包没有二进制 → 自动解压一次
    if [ ! -f "$BIN" ] && [ -f "$PKG" ]; then
        unpack
    fi
    [ -x "$BIN" ] || { echo "错误: 找不到可执行文件 $BIN（同目录也无 $PKG）" >&2; exit 1; }
    rm -f "$PIDFILE"
    cd "$DIR"   # 数据库 lz-monitor.db 与日志都落在部署目录，与调用位置无关
    if [ -f "$CONF" ]; then
        set -- "$BIN" -config "$CONF" "$@"
    else
        set -- "$BIN" "$@"
    fi
    nohup "$@" >/dev/null 2>&1 &
    echo $! > "$PIDFILE"
    sleep 1
    if ! pid_alive; then
        echo "错误: 启动失败，最近报错见 $DIR/lz-server.log" >&2
        echo "      （配置错误不落日志文件，前台运行排查: cd $DIR && ./lz-server -config server.yaml）" >&2
        extra=$(sweep_pids)
        if [ -n "$extra" ]; then
            echo "      （检测到已有实例仍在运行: pid $(echo "$extra" | tr '\n' ' ')——监听端口被占会导致新进程起不来）" >&2
        fi
        rm -f "$PIDFILE"
        exit 1
    fi
    echo "lz-server 已启动 (pid $(cat "$PIDFILE"))，日志: $DIR/lz-server.log"
}

# sweep_pids 兜底清扫候选——不经脚本启动的实例（手动 nohup / 开机自启 /
# pidfile 丢失）。两类匹配取并集：
#   1. cmdline 含「$DIR/lz-server + 空格或结尾」（脚本/绝对路径启动，任何平台可判）；
#   2. cmdline 含「lz-server + 空格或结尾」（./lz-server、裸名等相对形式，
#      不误伤 lz-server.sh/.old/.log 同名前缀）且 /proc cwd 在本部署目录
#      （Linux 限定；读不到 cwd 时该路返回空，退化到第 1 类，不误伤别处部署）。
sweep_pids() {
    {
        pgrep -f "$DIR/lz-server( |$)" 2>/dev/null || true
        for p in $(pgrep -f "lz-server( |$)" 2>/dev/null || true); do
            [ "$(readlink "/proc/$p/cwd" 2>/dev/null || true)" = "$DIR" ] && echo "$p"
        done
    } | sort -u
}

do_stop() {
    stopped=0
    if pid_alive; then
        pid=$(cat "$PIDFILE")
        kill "$pid" 2>/dev/null || true   # SIGTERM 优雅退出（完成 WAL checkpoint 再落盘）
        i=0
        while kill -0 "$pid" 2>/dev/null && [ "$i" -lt 10 ]; do
            sleep 1
            i=$((i + 1))
        done
        kill -9 "$pid" 2>/dev/null || true  # 10s 仍未退出则强杀
        rm -f "$PIDFILE"
        echo "lz-server 已停止"
        stopped=1
    else
        rm -f "$PIDFILE"
    fi
    # 兜底清扫：只按 pidfile 停会漏杀非脚本启动的旧进程，新实例因端口被占
    # 起不来，表现为「网页重启不生效」。
    extra=$(sweep_pids)
    if [ -n "$extra" ]; then
        for p in $extra; do
            kill "$p" 2>/dev/null || true
        done
        i=0
        while [ -n "$(sweep_pids)" ] && [ "$i" -lt 10 ]; do
            sleep 1
            i=$((i + 1))
        done
        for p in $(sweep_pids); do
            kill -9 "$p" 2>/dev/null || true
        done
        echo "已停止非脚本启动的旧进程（pid $(echo "$extra" | tr '\n' ' ' | sed 's/ *$//'))"
        stopped=1
    fi
    if [ "$stopped" = 0 ]; then
        echo "lz-server 未在运行"
    fi
    return 0
}

# unpack 解压压缩包到临时文件再 mv 就位（先解压后替换：包损坏时旧版不受影响；
# mv 原子替换不写运行中的文件，规避 Text file busy）
unpack() {
    [ -f "$PKG" ] || { echo "错误: 找不到压缩包 $PKG" >&2; exit 1; }
    if ! gzip -dc "$PKG" > "$BIN.tmp"; then
        rm -f "$BIN.tmp"
        echo "错误: 解压 $PKG 失败" >&2
        exit 1
    fi
    chmod +x "$BIN.tmp"
    if [ -f "$BIN" ]; then
        mv -f "$BIN" "$BIN.old"
        mv -f "$BIN.tmp" "$BIN"
        echo "已从 $(basename "$PKG") 解压更新二进制（旧版保留为 $(basename "$BIN").old）"
    else
        mv -f "$BIN.tmp" "$BIN"
        echo "已从 $(basename "$PKG") 解压二进制"
    fi
}

# 换二进制并重启：解压替换 → 停服务 → 启动新二进制。
# 运行中的进程持有旧文件 inode，scp/rsync 直接覆盖二进制会报 Text file busy——
# 所以分发改压缩包，解压成临时名后 mv 原子替换（停服前先解压，包损坏则服务不中断）。
do_upgrade() {
    pkg="${1:-$PKG}"   # 缺省取同目录 lz-server.gz（发布产物自带）
    if [ ! -f "$pkg" ]; then
        echo "用法: $0 upgrade [压缩包路径]（缺省用同目录 lz-server.gz）" >&2
        echo "错误: 找不到 $pkg" >&2
        exit 1
    fi
    case "$pkg" in /*) ;; *) pkg="$PWD/$pkg" ;; esac
    PKG="$pkg"
    unpack
    do_stop
    do_start
    echo "升级完成，旧版本保留为 lz-server.old（回滚: mv lz-server.old lz-server 后 restart）"
}

case "${1:-}" in
    start)   shift; do_start "$@" ;;
    stop)    do_stop ;;
    restart) shift; do_stop; do_start "$@" ;;
    upgrade) shift; do_upgrade "$@" ;;
    status)
        if pid_alive; then
            echo "运行中 (pid $(cat "$PIDFILE"))"
        else
            echo "未运行"
            exit 1
        fi
        ;;
    *)
        echo "用法: $0 {start|stop|restart|status|upgrade} [额外参数]" >&2
        exit 1
        ;;
esac
