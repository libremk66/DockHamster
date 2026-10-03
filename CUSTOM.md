# 定制分支说明（custom）

> 本分支 = 官方 [onlyLTY/dockerCopilot](https://github.com/onlyLTY/dockerCopilot)（v2.1.x）**+ 以下定制**。
> `latest` 分支永远保持与官方一致；**所有定制都在 `custom` 分支**，方便持续跟随上游更新。

## 定制功能

### 1. 白名单自动更新容器（含旧镜像自动清理）

- 由 `AutoUpdateCron` 定时触发（默认每天 04:00）
- 只更新 `AutoUpdateContainers` 白名单里的容器；**不配置 = 保持官方纯手动模式**
- 更新成功后自动清理**旧镜像**——三重安全条件（全部满足才删）：
  1. 没有任何容器（含已停止）引用它 → **多容器共用镜像时天然保护**
  2. 已无任何 tag（真正悬空）
  3. 不等于新镜像
- 环境变量 `DeleteOldImage=false` 可整体关闭清理

### 2. 多容器共用镜像修复（对应官方 issue #144 / #164 / #165）

- **检查层**：修复多 RepoDigests 时"结果被末位覆盖"的误报（官方 #165）；只比较同仓库 digest；自动清理已不存在镜像的过期状态（避免"更新后仍提示更新"）
- **并发安全**：镜像检查缓存在定时任务与 API 读取之间加了读写锁（原实现存在并发读写 map 的风险）
- **批量更新**：同一镜像被多个容器共用时**只拉取一次**，再逐个重建（对应 #164）
- 其中通用修复（尤其 #165）可以独立回馈给上游

## 新增环境变量

| 变量 | 默认 | 说明 |
|---|---|---|
| `AutoUpdateContainers` | 空 | 白名单，逗号分隔容器名（大小写不敏感）；`*` = 全部（除自身与排除名单）；空 = 不自动更新 |
| `AutoUpdateExclude` | 空 | 排除名单，逗号分隔；优先级高于白名单 |
| `AutoUpdateCron` | `0 4 * * *` | 自动更新的 cron 表达式（本地时区） |
| `DeleteOldImage` | `true` | 更新后是否清理旧镜像（`false` 关闭） |

示例（docker run / compose 的 environment 均可用）：

```
AutoUpdateContainers=jellyfin,filebrowser,alist,duplicati,qbittorrent,aria2-pro,ariang,syncthing-a,syncthing-b
AutoUpdateExclude=critical-db
```

## 跟随官方更新（保持 custom 不掉队）

```bash
# 一次性：加官方 remote
git remote add upstream https://github.com/onlyLTY/dockerCopilot.git

# 每次跟随时：
git fetch upstream latest
git rebase upstream/latest      # 定制提交较少且集中，冲突面很小
git push origin custom
```

- GitHub 网页上也可以直接对 fork 点 **"Sync fork"**（同步的是 `latest` 镜像分支）
- 与官方重叠的文件改动尽量保持小、单主题；通用修复（如 #165）建议顺手给官方发 PR

## 构建（GitHub Actions）

- 推送到本 `custom` 分支 → `.github/workflows/custom-build.yml` 自动构建并推送：
  `libremk66/dockercopilot-custom:latest`（同时打 `:vX.Y.Z` 版本 tag）
- 需在 fork 仓库的 **Settings → Secrets and variables → Actions** 配置：
  - `DOCKERHUB_USERNAME`：Docker Hub 用户名（libremk66）
  - `DOCKERHUB_TOKEN`：Docker Hub Access Token
- 首次推送后若 Actions 未运行：到 fork 的 Actions 页点 **"I understand my workflows, go ahead and enable them"**

## 部署（在 NAS 上）

把 DockerCopilot 容器的镜像由 `0nlylty/dockercopilot` 换成 `libremk66/dockercopilot-custom:latest`，
补上需要的环境变量（白名单等），其余挂载（docker.sock、/data）保持不变。
