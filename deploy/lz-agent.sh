#!/bin/sh
# lz-agent 启动/停止脚本（被监控机上的采集端）
#
# 部署：本脚本与 lz-agent 二进制、agent.yaml 放在同一目录（推荐 /home/lz-monitor/agent/，
#       与服务端目录 /home/lz-monitor/server/ 分开），可在任意位置调用，
#       路径一律相对脚本所在目录。
# 用法：./lz-agent.sh {start|stop|restart|status|upgrade}
#       二进制以 gzip 压缩包分发（同目录 lz-agent.gz，make release 产物自带）：
#       start 时无二进制会自动解压；upgrade [压缩包] 解压替换并重启——
#       rsync 整目录（或单独上传 .gz）后直接执行：
#       ./lz-agent.sh upgrade   （旧版保留为 lz-agent.old，回滚: mv 回去后 restart）
#
# 日志：默认零日志（agent.yaml 的 log_file: "off"）——不写任何日志文件，
#       agent 对磁盘零写入（担心监控磨损磁盘的场景默认即安全）。
#       需要排查问题时把 log_file 改为 agent.log：程序双写终端与文件并按
#       300MB 上限自动轮转（当前文件 + .1/.2 两个备份）。
#       数据出口（agent.yaml 的 mode）默认 pull=开只读指标端口等抓取；
#       改为 push 则主动上报服务端（server_url/token），不开端口。
set -eu

DIR=$(cd "$(dirname "$0")" && pwd)
BIN=$DIR/lz-agent
PKG=$DIR/lz-agent.gz   # 分发形态：gzip 压缩包（make release 产物自带）
PIDFILE=$DIR/lz-agent.pid
CONF=$DIR/agent.yaml   # 可选；缺省用内置默认值（:19100、无鉴权）

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

do_start() {
    if pid_alive; then
        echo "lz-agent 已在运行 (pid $(cat "$PIDFILE"))"
        return 0
    fi
    # 首次部署：目录里只有压缩包没有二进制 → 自动解压一次
    if [ ! -f "$BIN" ] && [ -f "$PKG" ]; then
        unpack
    fi
    [ -x "$BIN" ] || { echo "错误: 找不到可执行文件 $BIN（同目录也无 $PKG）" >&2; exit 1; }
    rm -f "$PIDFILE"
    cd "$DIR"   # 日志 agent.log 落在部署目录；agent.yaml 默认在二进制同目录查找
    [ -f "$CONF" ] || echo "提示: 未找到 ${CONF}，使用默认配置（:19100，无鉴权）"
    nohup "$BIN" >/dev/null 2>&1 &
    echo $! > "$PIDFILE"
    sleep 1
    if ! pid_alive; then
        echo "错误: 启动失败（默认零日志无文件可查；可临时前台运行 $BIN 排查，或在 agent.yaml 把 log_file 改为 agent.log）" >&2
        rm -f "$PIDFILE"
        exit 1
    fi
    echo "lz-agent 已启动 (pid $(cat "$PIDFILE"))"
}

do_stop() {
    if ! pid_alive; then
        echo "lz-agent 未在运行"
        rm -f "$PIDFILE"
        return 0
    fi
    pid=$(cat "$PIDFILE")
    kill "$pid" 2>/dev/null || true   # SIGTERM 优雅退出
    i=0
    while kill -0 "$pid" 2>/dev/null && [ "$i" -lt 10 ]; do
        sleep 1
        i=$((i + 1))
    done
    kill -9 "$pid" 2>/dev/null || true  # 10s 仍未退出则强杀
    rm -f "$PIDFILE"
    echo "lz-agent 已停止"
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
    pkg="${1:-$PKG}"   # 缺省取同目录 lz-agent.gz（发布产物自带）
    if [ ! -f "$pkg" ]; then
        echo "用法: $0 upgrade [压缩包路径]（缺省用同目录 lz-agent.gz）" >&2
        echo "错误: 找不到 $pkg" >&2
        exit 1
    fi
    case "$pkg" in /*) ;; *) pkg="$PWD/$pkg" ;; esac
    PKG="$pkg"
    unpack
    do_stop
    do_start
    echo "升级完成，旧版本保留为 lz-agent.old（回滚: mv lz-agent.old lz-agent 后 restart）"
}

case "${1:-}" in
    start)   do_start ;;
    stop)    do_stop ;;
    restart) do_stop; do_start ;;
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
        echo "用法: $0 {start|stop|restart|status|upgrade}" >&2
        exit 1
        ;;
esac
