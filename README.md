# 🐹 DockHamster · 容器仓鼠

<p align="center"><img src="docs/logo.png" width="112" alt="DockHamster"></p>

[![Docker Hub](https://img.shields.io/docker/v/libremk66/dockhamster?label=docker%20hub&logo=docker&logoColor=white)](https://hub.docker.com/r/libremk66/dockhamster)
[![Docker Pulls](https://img.shields.io/docker/pulls/libremk66/dockhamster)](https://hub.docker.com/r/libremk66/dockhamster)
[![License](https://img.shields.io/badge/license-AGPL--3.0-blue)](LICENSE)

> **Docker 容器管理面板 · 增强版**
> 基于 [onlyLTY/dockerCopilot](https://github.com/onlyLTY/dockerCopilot)（AGPL-3.0）二次开发，专注"省心的容器运维"：自动更新、旧镜像清理、整组更新、多渠道通知、实时进度、列表化界面。

## 目录

- [一、它是什么](#一它是什么)
- [二、相对原项目新增了什么](#二相对原项目新增了什么)
- [三、界面预览](#三界面预览)
- [四、快速开始](#四快速开始)
- [五、自动更新怎么用](#五自动更新怎么用)
- [六、上游与致谢](#六上游与致谢)
- [七、许可](#七许可)
- [📖 功能详解 · FEATURES.md](./FEATURES.md)

## 一、它是什么

一个自托管的 Docker 容器管理面板：启动 / 停止 / 重启、镜像管理与清理、备份恢复、容器更新检测，以及本版重点增强的**自动更新**能力。

- 纯 Docker 部署，单容器运行（内置前端，无需额外服务）
- 所有增强功能都可以不用：**不配置白名单 = 纯手动模式**，行为与原项目一致
- 配置持久化在 `/data/config/`，UI 保存即生效

## 二、相对原项目新增了什么

| 功能 | 说明 |
|---|---|
| ⚡ **自动更新（UI 配置）** | 白名单容器按 cron 计划自动更新；容器页列表一个开关即可加入/移出白名单；带运行记录与每容器最近结果 |
| 🗑️ **旧镜像自动清理** | 更新完成后自动清理悬空旧镜像；**多容器共用镜像受"引用计数"保护**（还有任何容器在用就绝不删） |
| 👥 **共用镜像整组更新** | 官方 issue [#144](https://github.com/onlyLTY/dockerCopilot/issues/144)/[#164](https://github.com/onlyLTY/dockerCopilot/issues/164) 场景：同一镜像被多个容器共用时，一键依次更新全部（**同一镜像只拉取一次**） |
| 🔔 **多渠道通知** | 更新成功/失败发送简报（含失败原因、清理统计、耗时）；支持**飞书 / 企业微信 / 钉钉 / Bark / Server酱 / Telegram / 自定义 Webhook**，每渠道独立测试 |
| 🗂️ **列表化界面 + 搜索** | 容器页 / 镜像页均为列表布局，状态、自动更新、可升级、操作一屏看清；容器按**名称/镜像**、镜像按**名称/Tag/ID** 实时搜索 |
| 📊 **实时进度** | 批量与单个更新全程可见：真实进度条 + **字节级拉取进度** + 心跳耗时；批量有「进行中」面板，单个更新在容器行内**整行展开进度子行** |
| 🏷️ **镜像快照与回滚** | 更新后的旧镜像可按策略处置：**自动清理**（默认）或**打快照保留**；容器页一键回滚到任意快照，**回滚前会自动给当前版本也打一份快照**（双向可回滚）；全局默认 + 每容器单独覆盖 |
| 📦 **容器迁移** | 三件套：**镜像体检**（找出换台机器就会丢的镜像）→ **打包导出**（docker save 镜像 + 容器配方 + compose + 一键导入脚本）→ **上传导入**（预检冲突，支持改名 / 端口重映射 / 卷路径映射）。迁移包自带 `import.sh`，**目标机没装面板也能还原** |
| 🩹 **上游修复** | ① 修复多 RepoDigests 时"永远提示有更新"（[#165](https://github.com/onlyLTY/dockerCopilot/issues/165) 同源问题）② 检查缓存并发安全 ③ 更新时保持容器原有运行状态 |

## 三、界面预览

**容器页**（列表行内即为自动更新开关与上次结果；更新中的容器在整行下方展开实时进度）：

![容器页](docs/screenshots/containers.png)

**镜像页**（总镜像 / 使用中 / 未使用 / 快照 / 无Tag 统计卡筛选，支持搜索）：

![镜像页](docs/screenshots/images.png)

**共用镜像整组更新**（点「更新」时自动识别共用同一镜像的其他容器）：

![共用镜像弹窗](docs/screenshots/group-update.png)

**快照与回滚**（更新前自动留快照；容器行点「回滚」即可回到任意历史版本）：

![回滚](docs/screenshots/rollback.png)

**容器迁移**（镜像体检找出"换机即丢"的镜像 → 打包 → 导入预检）：

![迁移体检](docs/screenshots/migrate-report.png)

![迁移导入预检](docs/screenshots/migrate-import.png)

**自动更新页**（计划 / 白名单 / 通知 / 运行记录）：

![自动更新页](docs/screenshots/auto-update.png)

## 四、快速开始

```yaml
# docker-compose.yml
services:
  dockhamster:
    image: libremk66/dockhamster:latest
    container_name: dockhamster
    restart: always
    environment:
      - TZ=Asia/Shanghai
      - secretKey=改成你自己的密钥   # 要求：大于8位且非纯数字
      # 可选：白名单初始值（仅首次生成配置时使用，之后以页面配置为准）
      # - AutoUpdateContainers=nginx,redis
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
      - ./data:/data
    ports:
      - "12712:12712"
```

```bash
docker compose up -d
# 浏览器打开 http://<你的主机>:12712 ，输入 secretKey 登录
```

## 五、自动更新怎么用

1. 打开侧栏 **「自动更新」** 页：勾选要自动更新的容器（或勾"全部容器"）、设定 cron 计划（默认每天 04:00）、按需配置通知渠道
2. 或直接在**容器页**列表里点某个容器的**自动更新开关**快捷加入白名单
3. 想验证效果：点「立即运行」，在「运行记录」里看结果
4. 建议：白名单先从**不怕重启的服务**开始（面板类/工具类），数据库等敏感容器用「排除名单」排除

💡 **共用同一镜像的多个容器**（例如两个 syncthing）建议**都勾上**：同一镜像只拉取一次，然后依次更新；旧镜像在最后一个使用者更新完成后才清理，未勾选的容器会一直提示「有新版本」。

⚠️ 自动更新会重建容器（等价于 `docker compose up -d` 的效果），请自行评估服务中断影响。

## 六、上游与致谢

DockHamster 的前身是原项目的一个增强分支，现已作为独立项目维护：

- 上游项目：[onlyLTY/dockerCopilot](https://github.com/onlyLTY/dockerCopilot)（后端）· [dongshull/Docker-Copilot-React](https://github.com/dongshull/Docker-Copilot-React)（前端），**版权归原作者所有**
- 本项目的多项通用修复已向上游提交 PR（[#166](https://github.com/onlyLTY/dockerCopilot/pull/166)），欢迎去官方 issue 下 +1
- 完整功能与 API 说明见 [FEATURES.md](./FEATURES.md)；上游原始 README 备份见 [README.upstream.md](./README.upstream.md)

## 七、许可

- 本项目遵循 **AGPL-3.0**（与上游一致，见 [LICENSE](./LICENSE)）。
- **完整源码即本仓库**：使用本镜像通过网络提供服务时，由此即可获取对应源码（AGPL §13）。
- 本分支为社区增强版，非官方发布；使用风险自负。
