# Changelog

本项目的用户可见变更记录。版本号以仓库根目录 `version` 文件为准。

## v1.8.1 — 2026-10-25

### 修复

- 更新完成后旧容器可能因 `restart=always` 或外部操作被重新拉起，导致新旧容器并存；现在先禁用旧容器重启、再次确认停止，并用 force 清理。
- 更新任务失败时，容器页弹出 daemon 实际错误（例如旧容器无法删除），不再静默清掉进度条。

### 新增

- 容器 / 镜像页按实际拉取来源显示 Docker Hub、GHCR、LinuxServer 文档入口；第三方 registry 与本地构建不出坏链。
- 容器页响应式列布局：≥1400px 五列表格，1024–1399px 卡片式，移动端纵向排列；镜像源独立成列，状态与可升级信息合并，名称 / 镜像列不再被挤压。

### 维护

- 移除前端仓库历史误跟踪的 `.DS_Store`、`.idea/*` 与 `dist/index.html`。

### 升级说明

- 无破坏性变更，无数据库 / 迁移。
- 镜像：`libremk66/dockhamster:v1.8.1`（`latest` 同步更新，多架构 amd64/arm64）。
- 更新方式：面板内更新 `dockhamster` 容器，或 `docker compose pull && docker compose up -d`。

[Unreleased]: https://github.com/libremk66/DockHamster/compare/v1.8.1...HEAD
[v1.8.1]: https://github.com/libremk66/DockHamster/compare/v1.8.0...v1.8.1
