#!/bin/bash
# PicoPanel Docker 操作脚本
# 使用: ./docker-start.sh [start|stop|restart|logs|backup|status]

set -e

COMPOSE_FILE="docker-compose.prod.yml"
SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; NC='\033[0m'
print_error()   { echo -e "${RED}❌ $1${NC}" >&2; }
print_success() { echo -e "${GREEN}✓ $1${NC}"; }
print_info()    { echo -e "${YELLOW}ℹ $1${NC}"; }

check_requirements() {
    command -v docker &> /dev/null || { print_error "Docker 未安装"; exit 1; }
    if ! docker compose version &> /dev/null; then
        print_error "Docker Compose 未安装（需要 Docker 20.10+）"
        exit 1
    fi
}

init_deployment() {
    print_info "初始化部署..."
    mkdir -p data logs

    if [ ! -f lm.idc ]; then
        openssl rand -base64 32 > lm.idc
        print_success "已生成实例密钥 lm.idc"
    fi

    if [ ! -f server.yaml ]; then
        print_error "缺少 server.yaml，请参考 deploy/server.example.yaml 创建"
        exit 1
    fi

    print_success "初始化完成，运行 $0 start 启动服务"
}

start_service() {
    [ -f server.yaml ] || { print_error "缺少 server.yaml"; exit 1; }
    [ -f lm.idc ]      || { print_error "缺少 lm.idc，请先运行: $0 init"; exit 1; }
    print_info "启动 PicoPanel..."
    docker compose -f "$COMPOSE_FILE" up -d
    print_success "已启动，查看日志: $0 logs"
}

stop_service() {
    print_info "停止 PicoPanel..."
    docker compose -f "$COMPOSE_FILE" down
    print_success "已停止"
}

backup_database() {
    print_info "备份数据库..."
    mkdir -p backups
    BACKUP="backups/lz-monitor.db.$(date +%Y%m%d_%H%M%S)"
    cp data/lz-monitor.db "$BACKUP"
    print_success "备份完成: $BACKUP"
}

show_help() {
    cat << EOF
使用: $0 [命令]

  init     初始化（创建目录和密钥文件）
  start    启动服务
  stop     停止服务
  restart  重启服务
  logs     查看实时日志
  backup   备份数据库到 backups/
  status   查看容器状态和资源
  help     显示帮助
EOF
}

cd "$SCRIPT_DIR"
check_requirements

case "${1:-help}" in
    init)    init_deployment ;;
    start)   start_service ;;
    stop)    stop_service ;;
    restart) stop_service; start_service ;;
    logs)    docker compose -f "$COMPOSE_FILE" logs -f picopanel-server ;;
    backup)  backup_database ;;
    status)
        docker compose -f "$COMPOSE_FILE" ps
        echo ""
        docker stats picopanel-server --no-stream
        ;;
    help)    show_help ;;
    *)       print_error "未知命令: $1"; show_help; exit 1 ;;
esac
