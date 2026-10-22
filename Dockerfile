# 多阶段构建：编译 + 运行
FROM golang:1.27-alpine AS builder

WORKDIR /build

# 安装依赖
RUN apk add --no-cache git make nodejs npm

# 复制项目文件
COPY . .

# 设置 Go 代理
ENV GOPROXY=https://goproxy.cn,direct

# 构建项目
RUN make server

# ============ 运行镜像 ============
FROM alpine:latest

# 安装运行时依赖和工具
RUN apk add --no-cache \
    ca-certificates \
    curl \
    tzdata \
    sqlite \
    tini

# 创建应用用户（非 root 运行）
RUN addgroup -g 1000 picopanel && \
    adduser -D -u 1000 -G picopanel picopanel

# 工作目录
WORKDIR /app

# 从构建阶段复制二进制文件
COPY --from=builder /build/bin/lz-server /app/lz-server

# 创建数据目录
RUN mkdir -p /data /var/log/picopanel && \
    chown -R picopanel:picopanel /app /data /var/log/picopanel

# 健康检查
HEALTHCHECK --interval=30s --timeout=10s --retries=3 --start-period=10s \
    CMD curl -f http://localhost:8080/ || exit 1

# 使用 tini 作为 init，确保信号处理正确
ENTRYPOINT ["/sbin/tini", "--"]

# 默认命令
CMD ["/app/lz-server", "-config", "/etc/picopanel/server.yaml"]

# 元数据
LABEL maintainer="PicoPanel" \
      description="PicoPanel - Linux Server Monitoring System" \
      org.opencontainers.image.source="https://github.com/lyxizq/picopanel-release"

# 暴露端口
EXPOSE 8080
