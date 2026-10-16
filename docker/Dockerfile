# 使用 Alpine 作为基础镜像（固定 tag，避免 latest 漂移）
FROM alpine:3.21
LABEL authors="onlyLTY, libremk66"
WORKDIR /app

ARG TARGETPLATFORM

COPY dc-back/dist/$TARGETPLATFORM/dockhamster ./dockhamster
COPY dc-back/etc/. ./etc
COPY start.sh ./start.sh

# 安装 docker compose plugin（compose 感知更新需要容器内可执行 compose 命令）
RUN apk add --no-cache tzdata docker-cli-compose \
    && chmod +x start.sh \
    && chmod +x dockhamster

# 设置环境变量（不提供 secretKey 默认值：空密钥会让面板静默失去鉴权。
# 缺少该变量时由程序启动校验明确拒绝并退出，见 dockhamster.go；部署时必须传入）
ENV DOCKER_HOST="unix:///var/run/docker.sock" \
    BACKUP_DIR="/data/backups" \
    TZ="Asia/Shanghai"

VOLUME ["/data"]
# 设置启动命令
CMD ["./start.sh"]