# 使用 Alpine 作为基础镜像
FROM alpine
LABEL authors="onlyLTY"
WORKDIR /app

ARG TARGETPLATFORM

COPY dc-back/dist/$TARGETPLATFORM/dockerCopilot ${WORKDIR}/dockerCopilot
COPY dc-back/etc/. ${WORKDIR}/etc
COPY start.sh ${WORKDIR}/start.sh

RUN apk add --no-cache tzdata \
    && chmod +x start.sh

# 设置环境变量
ENV secretKey="" \
    DOCKER_HOST="unix:///var/run/docker.sock" \
    BACKUP_DIR="/data/backups" \
    TZ="Asia/Shanghai"

VOLUME ["/data"]
# 设置启动命令
CMD ["./start.sh"]