# DockHamster 功能说明

> DockHamster（容器仓鼠）基于 [onlyLTY/dockerCopilot](https://github.com/onlyLTY/dockerCopilot)（AGPL-3.0）二次开发，
> 在原项目基础上叠加以下增强；不带增强配置时行为与原项目一致（纯手动模式）。
>
> 配套前端：[libremk66/DockHamster-UI](https://github.com/libremk66/DockHamster-UI)。

## 一、功能总览

### 1. 自动更新（UI 配置 + 定时执行）

- **入口**：侧栏「自动更新」页 + 容器页列表的自动更新开关
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

### 4. 多渠道通知（7 个渠道）

- 更新流程结束后发送简报（有内容才发）：成功清单、失败原因、清理旧镜像数、耗时
- 渠道：**飞书 / 企业微信 / 钉钉 / Bark / Server酱 / Telegram / 自定义 Webhook**
  - 飞书、钉钉支持「加签」密钥；企业微信自动按 2048 字节截断
  - Telegram 可自填 API 反代地址；自定义 Webhook 支持 GET/POST、自定义请求头与 body 模板（`{title}` `{text}` 变量）
- 「自动更新」页逐渠道勾选启用、每渠道**独立测试发送**（用当前表单值直接发，无需先保存）
- 两个总开关：更新成功时发送 / 出现失败时发送
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

### 6. 实时进度展示

- 批量运行（定时/手动）：「自动更新」页出现「进行中」面板——每容器一行实时进度条 + 阶段文案 + 拉取细节（运行期 2 秒刷新，点「立即运行」后立即显示）
- 拉取阶段使用**真实字节百分比**（Docker 进度流 `current/total` 映射 5%~60%），并带**心跳**（每 2 秒刷新已耗时），长任务不再"看起来卡死"
- 单容器更新：容器行内**整行展开进度子行**（进度条 + 阶段 + 字节级细节 + 百分比），信息全宽可见
- 前端轮询策略：运行中 2 秒 / 空闲 10 秒；15 分钟兜底 + 真正 3 分钟无任何变化才暂停刷新（大镜像不再"假超时"）

### 7. 列表化界面与搜索

- **容器页**：一行 = 一个容器，列：名称/镜像 · 状态 · 自动更新（行内开关 + 上次结果）· 可升级 · 操作；更新中该行下方展开进度子行
- **镜像页**：一行 = 一个镜像，列：镜像 · 大小 · 使用情况（使用中/未使用药丸）· 创建时间 · 操作（Hub / 删除 / 强删）；悬空镜像标注"无标签（悬空）"
- **搜索**：容器按名称/镜像、镜像按名称/Tag/ID 实时过滤，带"筛选出 X / 共 Y"计数与无结果提示
- 交互保持不变：点行打开详情，Ctrl/Cmd+点击或批量模式勾选；统计卡筛选与搜索叠加生效
- 移动端自动折行排版（每行折为「标题行 + 信息行 + 操作行」）

### 8. 镜像快照与回滚

更新容器时，**旧镜像**（更新前那一份）有三种处置方式，可全局设默认、也可给单个容器指定：

| 策略 | 行为 |
|---|---|
| **清理**（默认） | 满足安全条件（无容器引用、无标签、非新镜像）才删除，省空间 |
| **打快照保留** | 给旧镜像打上 `dh-snap/<名字>:<日期>-<时分>` 标签 → 天然免于自动清理，可随时回滚 |
| 保留不处理 | 什么都不做（旧行为） |

- **多容器共用镜像时"保留优先"**：任何一个容器要求快照，就给旧镜像打快照
- **保留数量**：每个镜像默认只留最近 3 个快照，超量自动清理（走同样的安全条件）
- **回滚**：容器行出现「回滚」按钮（仅当有可用快照）→ 选一个快照 → 用它重建容器
  - **回滚前会自动给当前版本也打一份快照** → 回滚可逆（双向）
  - 回滚复用标准更新流程（重建 + 保持原运行状态），全程进度条
- **占用可见**：自动更新页显示快照数量 / 占用 / 系统盘剩余；镜像页新增「快照」分类可批量清理
- 快照打的是**独立命名空间**（默认 `dh-snap/`），不参与"更新检测"，也不会被旧镜像清理删掉

### 9. 容器迁移（本地包）

三步闭环，全程零外部依赖（不需要 registry、不需要联网）：

**① 镜像体检** — 扫描全部镜像并分类：

| 分类 | 判定 | 含义 |
|---|---|---|
| 公共可得 | 有 RepoDigests（从仓库拉过） | 目标机可直接 pull，无需搬运 |
| 本地构建 | 有 tag 但无 digest | 换机即丢，**必须打包搬运** |
| 悬空 | 无任何 tag | 连名字都没有，先打标签才能搬运 |

**② 打包导出** — 选中容器/镜像 → 生成单个 `.tar.gz` 迁移包，内含：

```
manifest.json      机器可读清单（镜像摘要 + 容器配方 + 校验和）
images/*.tar.gz    docker save 出来的镜像（流式导出，字节级进度）
compose.yaml       可直接使用的 compose 文件（含全部容器定义）
import.sh          无面板一键导入脚本（只用标准 docker 命令）
README.txt         场景化导入指引（有面板 / 只有 Docker / 手工）
checksums.txt      校验和（sha256sum -c 可验证完整性）
```

- 可选：镜像压缩（gzip）、环境变量脱敏（密码/Token 类默认替换为 ****）
- 容器配置原样保存，保证精确重建

**③ 上传导入** — 上传包 → **预检（dry-run）** → 执行：

- 预检逐容器给出：镜像可得性（本地已有 / 包内提供 / 可拉取 / 缺失）、名字冲突（自动建议新名）、端口冲突（谁占用了）、卷路径状态（**确不存在** / **面板不可见待确认**，后者附同名目录映射建议）
- 覆盖项：容器改名、宿主端口重映射、卷路径重映射、缺失目录自动创建、导入后自动启动（仅启动原本在运行的）
- 执行时按需 `docker load` 包内镜像 → 应用覆盖 → 重建容器，进度实时可见

**无面板导入**（换机迁移的常见情形）：包内 `import.sh` 支持 `--dry-run`（只检查：磁盘空间 / 校验和 / 卷路径 / compose 语法），确认后直接执行即可；也可把 `compose.yaml` 粘进 Portainer 等其它面板。

> 架构说明：镜像搬运记录使用 `transports[]` 数组（当前实现 `archive`；`registry` 为预留扩展位），导入端按 `本地已有 → 包内 → registry` 顺序兜底——后续接入自建 registry / Docker Hub 私有仓库是纯增量。

## 二、环境变量（仅初始默认值）

环境变量只在 `/data/config/autoUpdate.json` **不存在时**用于生成初始配置；之后一律以页面配置为准。

| 变量 | 默认 | 说明 |
|---|---|---|
| `AutoUpdateContainers` | 空 | 白名单初始值，逗号分隔容器名；`*`=全部；不为空时初始 `enabled=true` |
| `AutoUpdateExclude` | 空 | 排除名单初始值，逗号分隔；优先级高于白名单 |
| `AutoUpdateCron` | `0 4 * * *` | cron 表达式初始值 |
| `DeleteOldImage` | `true` | 旧镜像清理初始开关（`false` 关闭） |
| `FeishuWebhook` | 空 | 飞书 Webhook 初始值（写入 notify.feishu） |
| `AutoUpdateConfigFile` | `/data/config/autoUpdate.json` | 配置文件路径覆盖 |
| `DelOldContainer` | `true` | 既有变量：更新后是否删除旧容器（`false` 保留改名后的旧容器） |

## 三、API 一览（定制部分）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/autoUpdate/settings` | 读取自动更新设置 |
| POST | `/api/autoUpdate/settings` | 保存设置（会校验 cron 并重注册定时任务） |
| POST | `/api/autoUpdate/run` | 立即运行一轮（异步） |
| GET | `/api/autoUpdate/status` | 运行状态 + 最近 30 次记录 + 每容器最近结果 |
| POST | `/api/autoUpdate/testNotify` | 渠道测试发送（`{channel, config}`；config 为前端当前表单值） |
| POST | `/api/container/:id/updateGroup` | 整组更新（更新与该容器共用同一镜像的所有容器，返回各容器任务 ID） |
| GET | `/api/snapshot/list` | 快照列表 + 占用统计（数量/大小/磁盘剩余/保留数量） |
| POST | `/api/snapshot/create` | 手动给容器当前镜像打快照（`{containerName}` 或 `{imageId}`） |
| POST | `/api/snapshot/rollback` | 回滚到快照（`{containerName, ref}`，回滚前自动快照当前版本） |
| POST | `/api/snapshot/prune` | 按保留数量清理超量快照（`{keep}` 可选） |
| DELETE | `/api/snapshot?refs=a,b` | 删除指定快照（被容器使用的会拒绝） |
| GET | `/api/migrate/images/report` | 镜像体检（分类 + 风险统计 + 磁盘剩余） |
| POST | `/api/migrate/images/tag` | 给镜像打可搬运标签（`{imageId, ref}`，同名指向他人时拒绝覆盖） |
| POST | `/api/migrate/exports` | 生成迁移包（异步，返回 taskID） |
| GET | `/api/migrate/exports` | 迁移包列表 |
| GET | `/api/migrate/exports/download?file=` | 流式下载（支持断点续传） |
| DELETE | `/api/migrate/exports?file=` | 删除迁移包 |
| POST | `/api/migrate/imports/upload` | 上传迁移包（multipart，≤10GB） |
| POST | `/api/migrate/imports/plan` | 导入预检（dry-run，`{file}`） |
| POST | `/api/migrate/imports/apply` | 执行导入（`{file, items[], start, autoCreateDirs}`，异步） |

## 四、代码位置（定制改动集中在）

```
internal/module/autosettings.go    新增：设置存储 + 运行状态
internal/module/notify.go          新增：多渠道通知发送（7 渠道）
internal/utiles/auto_update.go     新增：白名单自动更新（批量、按镜像分组）
internal/utiles/group_update.go    新增：整组更新
internal/utiles/cleanup.go         新增：旧镜像安全清理
internal/logic/autoupdate/         新增：设置/运行/状态/测试 逻辑 + cron 重注册
internal/handler/autoupdate/       新增：handlers
internal/module/migrate.go          新增：迁移包模型 + 搬运抽象（transports[] 多路线扩展位）
internal/utiles/snapshot.go         新增：镜像快照（打标签/保留清理/占用统计/防覆盖）
internal/utiles/migrate_classify.go 新增：镜像体检分类 + 可搬运标签
internal/utiles/migrate_export.go   新增：迁移包导出（流式 save + 打包 + 进度）
internal/utiles/migrate_import.go   新增：导入预检 + 执行（load/覆盖/重建）
internal/utiles/migrate_artifacts.go / migrate_templates.go  新增：compose 生成 + import.sh/README 模板
internal/logic/snapshot/ + handler/snapshot/   新增：快照 API
internal/logic/migrate/ + handler/migrate/     新增：迁移 API
internal/module/checkupdate.go     修改：digest 判定修复 + 并发锁 + 过期清理
internal/utiles/updatecontainer.go 修改：更新选项、状态保持、清理挂钩
internal/utiles/getcontainerlist.go 修改：改用并发安全的查询方法
internal/handler/routes.go         修改：注册新路由
internal/logic/container/*.go      修改：手动更新接入设置；容器列表增加 imageId
internal/types/types.go / svc / dockercopilot.go  小改
```

前端（[libremk66/DockHamster-UI](https://github.com/libremk66/DockHamster-UI)）：
`src/components/AutoUpdate.jsx`（新增页面：白名单/通知/进度/旧镜像策略）、`Containers.jsx`（列表化/搜索/开关/整组弹窗/进度子行/回滚入口）、`Images.jsx`（列表化/搜索/快照分类）、`Migrate.jsx`（迁移页：体检/导出/导入）、`ProgressBar.jsx`、`Header.jsx`、`App.jsx`、`api/client.js`。

## 五、版本与发布机制

**版本号三处一致**：仓库根目录的 `version` 文件是唯一事实来源（如 `v1.3.0`），一次提交同时驱动三件事：

1. **镜像构建**：`build.yml` 构建并推送 `libremk66/dockhamster:latest` + `:v1.3.0`（多架构）
2. **GitHub Release**：`release.yml` 自动打 tag、创建 Release 并生成变更日志（发版零手工步骤）
3. **面板内的更新提示**：面板读取同一个 `version` 文件来判断是否有新版

**面板怎么知道有新版本**（双保险，任一命中即提示）：

- **版本号比较**：拉取仓库 main 分支的 `version` 文件（**多源自动兜底**：GitHub raw → jsDelivr CDN → 公共镜像站，每源 6 秒超时、10 分钟缓存、记住上次可用的源）——**无需任何代理配置**
- **镜像 digest 比对**：直接问镜像仓库"`libremk66/dockhamster:latest` 的 digest 变了没"（复用容器更新检测的同一套逻辑）——防止某次提交忘了改版本号，用户仍能收到提示

**更新方式**（Docker 部署）：容器页找到 `dockhamster` 容器点「更新」，或 `docker compose pull && docker compose up -d`。面板不做"自己更新自己"（避免更新过程中断），侧栏的「有新版本」按钮会直接给出这两种方式。

## 六、与上游的关系

本项目为独立维护的社区增强版：**不再自动 rebase 上游**，但保留了完整的 git 历史与出处标注（AGPL 要求）；上游修复会按需 cherry-pick。

通用修复建议持续向上游提 PR；一旦合并，对应实现即可与上游对齐。

## 七、构建与发布

- 推送 `main` 分支 → GitHub Actions（`build.yml`）自动构建 amd64/arm64 并推送：
  `libremk66/dockhamster:latest`（同时打 `:vX.Y.Z` 版本 tag）
- 需要仓库 Secrets：`DOCKERHUB_USERNAME`、`DOCKERHUB_TOKEN`

## 八、许可

遵循 **AGPL-3.0**（与上游一致）。完整源码即本仓库（`main` 分支），AGPL §13 合规。版权归属：上游原作者 + 本项目贡献者。
