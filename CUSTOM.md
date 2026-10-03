# 定制功能说明（custom 分支）

> 本分支 = 官方 [onlyLTY/dockerCopilot](https://github.com/onlyLTY/dockerCopilot)（`latest`）**+ 以下增强**。
> `latest` 分支永远保持与官方一致；所有定制都集中在 `custom` 分支，便于持续跟随上游。

## 一、功能总览

### 1. 自动更新（UI 配置 + 定时执行）

- **入口**：侧栏「自动更新」页 + 容器卡片 ⚡ 快捷开关
- 按 `cron` 计划（默认 `0 4 * * *`）自动更新白名单容器；**不配置白名单 = 保持官方纯手动模式**
- 配置持久化在 `/data/config/autoUpdate.json`（UI 保存即生效；环境变量仅作首次生成时的默认值）
- 运行记录（最近 30 次：时间/触发方式/成功失败明细/清理统计/耗时）+ 每容器最近一次结果

### 2. 旧镜像安全清理

更新成功后自动清理被替换的旧镜像。**三重条件全部满足才删**（否则跳过）：

1. 没有任何容器（含已停止/已重命名的旧容器）引用它 → **多容器共用镜像的引用计数保护**
2. 已无任何 RepoTag（真正悬空）
3. 不等于更新后的新镜像

开关在「自动更新」页（`deleteOldImage`，默认开启）。

### 3. 共用镜像整组更新（对应官方 issue #144 / #164）

同一镜像被多个容器共用时，点某个容器的「更新」会弹窗提示：

> 该镜像还被 N 个容器共用：… 是否一并更新？〔取消〕〔仅此容器〕〔**全部更新**〕

「全部更新」的行为：

- **同一镜像只拉取一次**，再逐个容器重建（解决重复拉取）
- 每个容器保持各自原有运行/停止状态
- 旧镜像清理遵循引用计数保护，最后一个使用者更新完成后才真正删除

### 4. 飞书通知

- 更新流程结束后发送简报（有内容才发）：成功清单、失败原因、清理旧镜像数、耗时
- 「自动更新」页配置 Webhook + 两个开关（成功发送 / 失败发送）+ 一键测试发送
- 简报示例：
  ```
  🔄 DockerCopilot 自动更新 2026-10-03 04:00
  ✅ 已更新 2 个：jellyfin、filebrowser
  🗑️ 清理旧镜像 1 个
  ⚠️ 失败 1 个：
    - qbittorrent：拉取超时
  ⏱ 耗时 42.5s
  ```

### 5. 上游修复（可独立回馈上游）

| 修复 | 说明 | 对应 issue |
|---|---|---|
| 多 RepoDigests 误报 | 原逻辑循环末位覆盖，导致"更新完仍提示更新"；改为"任一本地 digest 匹配远端即视为最新"，且只比较同仓库 digest | [#165](https://github.com/onlyLTY/dockerCopilot/issues/165)（同族：[#142](https://github.com/onlyLTY/dockerCopilot/issues/142)/[#87](https://github.com/onlyLTY/dockerCopilot/issues/87)/[#56](https://github.com/onlyLTY/dockerCopilot/issues/56)） |
| 并发安全 | 检查缓存（按镜像 ID 的 map）在定时任务与 API 读取间加读写锁，并自动清理已不存在镜像的过期条目 | — |
| 状态保持 | 更新时若容器原为停止状态，重建后保持停止，不再"顺手启动" | — |

## 二、环境变量（仅初始默认值）

环境变量只在 `/data/config/autoUpdate.json` **不存在时**用于生成初始配置；之后一律以页面配置为准。

| 变量 | 默认 | 说明 |
|---|---|---|
| `AutoUpdateContainers` | 空 | 白名单初始值，逗号分隔容器名；`*`=全部；不为空时初始 `enabled=true` |
| `AutoUpdateExclude` | 空 | 排除名单初始值，逗号分隔；优先级高于白名单 |
| `AutoUpdateCron` | `0 4 * * *` | cron 表达式初始值 |
| `DeleteOldImage` | `true` | 旧镜像清理初始开关（`false` 关闭） |
| `FeishuWebhook` | 空 | 飞书 Webhook 初始值 |
| `AutoUpdateConfigFile` | `/data/config/autoUpdate.json` | 配置文件路径覆盖 |
| `DelOldContainer` | `true` | 既有变量：更新后是否删除旧容器（`false` 保留改名后的旧容器） |

## 三、API 一览（定制部分）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/autoUpdate/settings` | 读取自动更新设置 |
| POST | `/api/autoUpdate/settings` | 保存设置（会校验 cron 并重注册定时任务） |
| POST | `/api/autoUpdate/run` | 立即运行一轮（异步） |
| GET | `/api/autoUpdate/status` | 运行状态 + 最近 30 次记录 + 每容器最近结果 |
| POST | `/api/autoUpdate/testNotify` | 发送飞书测试消息（可临时覆盖 webhook） |
| POST | `/api/container/:id/updateGroup` | 整组更新（更新与该容器共用同一镜像的所有容器，返回各容器任务 ID） |

## 四、代码位置（定制改动集中在）

```
internal/module/autosettings.go    新增：设置存储 + 运行状态
internal/module/feishu.go          新增：飞书发送
internal/utiles/auto_update.go     新增：白名单自动更新（批量、按镜像分组）
internal/utiles/group_update.go    新增：整组更新
internal/utiles/cleanup.go         新增：旧镜像安全清理
internal/logic/autoupdate/         新增：设置/运行/状态/测试 逻辑 + cron 重注册
internal/handler/autoupdate/       新增：handlers
internal/module/checkupdate.go     修改：digest 判定修复 + 并发锁 + 过期清理
internal/utiles/updatecontainer.go 修改：更新选项、状态保持、清理挂钩
internal/utiles/getcontainerlist.go 修改：改用并发安全的查询方法
internal/handler/routes.go         修改：注册新路由
internal/logic/container/*.go      修改：手动更新接入设置；容器列表增加 imageId
internal/types/types.go / svc / dockercopilot.go  小改
```

前端（[libremk66/Docker-Copilot-React](https://github.com/libremk66/Docker-Copilot-React) `custom` 分支）：
`src/components/AutoUpdate.jsx`（新增页面）、`Containers.jsx`（开关/徽标/整组弹窗）、`Header.jsx`、`App.jsx`、`api/client.js`。

## 五、跟随官方更新

```bash
# 后端
git fetch upstream latest && git rebase upstream/latest && git push origin custom

# 前端（另一仓库，上游为 dongshull/Docker-Copilot-React 的 master）
git fetch upstream master && git rebase upstream/master && git push origin custom
```

通用修复建议持续向上游提 PR；一旦合并，对应文件就无需再维护。

## 六、构建与发布

- 推送 `custom` 分支 → GitHub Actions（`custom-build.yml`）自动构建 amd64/arm64 并推送：
  `libremk66/dockercopilot-custom:latest`（同时打 `:vX.Y.Z` 版本 tag）
- 需要仓库 Secrets：`DOCKERHUB_USERNAME`、`DOCKERHUB_TOKEN`

## 七、许可

遵循 **AGPL-3.0**（与上游一致）。完整源码即本仓库（`custom` 分支），AGPL §13 合规。
