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

**更新检测（"看"）与自动更新（"做"）是两条独立链路**

- **更新检测**：定期探测 registry，刷新容器/镜像页的「有新版本」角标 —— 只读、不碰容器
  - 频率可配（「自动更新」页 → 更新检查频率，默认 `30 * * * *` 每小时），与自动更新 cron 各自独立
  - **走 Docker 守护进程通道**（`DistributionInspect`）：与"点更新时实际拉取"走同一条网络路径，拿到的就是实时 digest；失败才回退面板自建请求。这样国内环境不会卡在第三方加速站的缓存上
  - **「检查更新」按钮**：容器页 / 镜像页 / 自动更新页都有，点一下立即探测（并发检查，50+ 镜像约 20 秒），完成后弹结果并刷新角标
- **自动更新**：到点执行更新（拉镜像 + 重建容器），受总开关控制
- 建议：检查频率 ≥ 更新频率（勤看、慎动）

**共用同一镜像的多个容器，勾一个还是勾两个？**

- **勾两个（推荐）**：同一镜像**只拉取一次**，然后**依次**更新两个容器（各自保持原有配置与运行状态）。旧镜像有引用计数保护：第一个更新完时还被第二个用着 → 不删；最后一个也更新完后才清理（若策略为"清理"）
- **只勾一个**：只有它会被更新；另一个继续跑旧镜像，并在容器页**持续显示「有新版本」**提醒你；旧镜像因仍被引用而保留（会多占一份空间）
- **手动一次性更新**：容器页点任一共用容器的「更新」→ 弹窗选「**全部更新**」，走的是同一套逻辑（只拉一次 + 逐个重建）
- 多个容器的旧镜像策略不一致时（如一个"清理"、一个"打快照"），按**保留优先**合并，不会把要留的快照清掉

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
- 定时自动更新里的"勾选白名单"是同一套机制：**共用镜像的容器建议都勾上**（详见第 1 节）

### 4. 多渠道通知（8 个渠道）

- 更新流程结束后发送简报（有内容才发）：成功清单、失败原因、清理旧镜像数、耗时
- 渠道：**飞书 / 企业微信 / 钉钉 / QQ（官方机器人）/ Bark / Server酱 / Telegram / 自定义 Webhook**
  - 企业微信支持两种模式（二选一）：**应用消息**（CorpID + Secret + AgentID，可推送到个人微信；长内容自动按 2048 字节分行分块逐条发送）或**群机器人 Webhook**
  - 飞书支持两种模式（二选一）：**自建应用**（App ID + App Secret，消息为 Markdown 交互卡片、标题按内容自动红/绿/蓝着色；发送失败自动回退纯文本）或**群机器人 Webhook**（支持加签）
  - QQ 走官方机器人（AppID + ClientSecret，Markdown 优先、失败回退纯文本）——⚠️ 官方限制：主动消息每月限 4 条/群、4 条/用户，且目标需先与机器人交互过
  - 钉钉支持「加签」密钥；企业微信自动按 2048 字节截断
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

### 10. 更新健康校验与自动回滚

更新的最后一步是**健康校验**：启动新容器后连续 3 次检查处于运行且未重启（约 6 秒）即通过；容器自带 `HEALTHCHECK` 时按它的结果等待（上限约 90 秒）。

出现以下任一情况判失败并**自动回滚**：启动后退出、反复重启（Restarting）、被 OOM 杀掉、健康检查 unhealthy。回滚动作：删除不健康的新容器 → 把备份的旧容器改回原名并启动（原本是停止状态的保持停止）。**创建新容器失败、启动失败同样回滚**——更新失败不会把服务撂倒。校验中间状态实时显示在进度里（`新容器运行正常（1/3）`）。

### 11. 容器异常守护

面板每 60 秒巡检一次（首次采样只建基线，避免启动时误报）：

- **异常退出**：退出码非 0 或被 OOM 杀掉 → 告警；退出码 0 视为正常停止，不打扰
- **首见即近期崩溃**：面板重启期间崩掉、开机后起不来的容器也补报（10 分钟窗口）
- **反复重启**：10 分钟内重启 ≥3 次 → 告警
- **恢复运行**：报过故障的容器重新跑起来 → 一条恢复通知

面板主动发起的停止/重启/更新会先登记**静默窗口**（停止 10 分钟 / 重启 3 分钟 / 更新 15 分钟），窗口内不告警；同容器同类告警 30 分钟冷却。开关在「自动更新页 → 通知卡片 → 容器异常告警」（默认开），走同一套通知渠道。

### 12. 镜像加速源 + 加速拉取

- 加速源列表可增删，**一键并发测速**（对 `https://<源>/v2/` 探针，200/401 视为可用），按延迟排序，可设默认源
- 「**加速拉取**」：把 Docker Hub 镜像按 `源/library/nginx:tag` 拉取，完成后自动打回原始镜像名并清理临时标签；仅 Docker Hub 镜像可用（ghcr.io 等不显示入口）
- 「**更新时自动走加速源**」：开启后更新流程优先走默认加速源，失败自动回退直连；非 Hub 镜像不受影响
- 配置持久化在 `/data/config/accelerator.json`

### 13. 面板自更新（接力容器）

容器无法在自身进程内"停掉自己再重建"（stop 自己的瞬间流程即中断），因此用一次性**接力容器**完成替换：

1. 主容器拉取新镜像（走加速源配置）
2. 用**新镜像**启动接力容器（挂 docker.sock 与 /data，AutoRemove）
3. 接力容器：停旧容器 → 改名备份 → 用旧容器完整配置 + 新镜像重建同名容器 → 启动并校验稳定性（连续 3 次检查）
4. 成功删备份；失败自动回滚旧容器；结果写入 /data，由新面板启动时上报（日志 + 通知）

入口：侧栏「有新版本」弹窗的「**立即更新面板**」；容器页对 `dockhamster` 容器点「更新」也会自动切换到该流程（容器行带「本面板」标识）。前提是新镜像包含该逻辑（v1.4.0 起）。

### 14. Web 端口直达 + favicon

- `/api/containers` 增加 `ports` 字段（容器对外发布的 TCP 端口：去重、升序、**跳过仅绑回环的**）；容器行渲染为可点按钮，点击新标签打开 `http://<当前面板主机>:<端口>`
- 图标优先级：容器自带/内置/自定义 logo → 容器网页 favicon（后端解析 `<link rel=icon>`，前端缓存 7 天）→ 渐变占位

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
| `AcceleratorConfigFile` | `/data/config/accelerator.json` | 加速源配置路径覆盖 |
| `DelOldContainer` | `true` | 既有变量：更新后是否删除旧容器（`false` 保留改名后的旧容器） |

## 三、API 一览（定制部分）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/autoUpdate/settings` | 读取自动更新设置 |
| POST | `/api/autoUpdate/settings` | 保存设置（会校验 cron 并重注册定时任务） |
| POST | `/api/autoUpdate/run` | 立即运行一轮（异步） |
| GET | `/api/autoUpdate/status` | 运行状态 + 最近 30 次记录 + 每容器最近结果 |
| POST | `/api/autoUpdate/testNotify` | 渠道测试发送（`{channel, config}`；config 为前端当前表单值） |
| POST | `/api/autoUpdate/check` | 立即检查一轮镜像更新（同步，返回 `{checked, needUpdate}`） |
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
| GET | `/api/accelerator/settings` | 读取加速源配置（列表 / 默认源 / 更新时自动加速开关） |
| POST | `/api/accelerator/settings` | 保存加速源配置 |
| POST | `/api/accelerator/test` | 并发测速（`{sources[]}` 可选，空则测当前列表；返回延迟与可用性） |
| POST | `/api/accelerator/pull` | 加速拉取镜像（`{source, image}`，异步返回 taskID，进度走 `/api/progress/:id`） |
| GET | `/api/selfUpdate/status` | 面板自更新状态（当前版本 / 自身容器 / 上次更新结果） |
| POST | `/api/selfUpdate/run` | 一键面板自更新（异步返回 taskID） |
| GET | `/api/favicon/resolve?url=` | 解析目标页面 favicon（抓 `<link rel=icon>`，失败回落 /favicon.ico） |

## 四、代码位置（定制改动集中在）

```
internal/module/autosettings.go    新增：设置存储 + 运行状态
internal/module/notify.go          新增：多渠道通知发送（8 渠道；飞书/QQ 支持官方应用模式）
internal/module/notify_feishu_app.go 飞书应用推送（tenant_access_token + 交互卡片，失败回退文本）
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
internal/utiles/updatecontainer.go 修改：更新选项、状态保持、清理挂钩；健康校验与回滚接入
internal/utiles/getcontainerlist.go 修改：改用并发安全的查询方法
internal/handler/routes.go         修改：注册新路由
internal/logic/container/*.go      修改：手动更新接入设置；容器列表增加 imageId / ports / isSelf；更新自身切换到接力自更新
internal/types/types.go / svc / dockhamster.go  小改
internal/selfupdate/               新增：面板自更新（接力容器 relay + 主容器侧 launch + 开机结果上报）
internal/watchdog/watchdog.go      新增：容器异常守护巡检（退出/OOM/重启循环/恢复）
internal/module/accelerator.go     新增：加速源配置存储
internal/utiles/imageref.go        新增：Docker Hub 镜像引用解析（加速源前缀）
internal/utiles/pullviaaccelerator.go 新增：加速拉取与"更新时自动加速"（失败回退直连）
internal/utiles/healthcheck.go     新增：更新后健康校验 + 回滚（含静默窗口登记）
internal/utiles/publishedports.go  新增：容器对外端口提取（供端口直达）
internal/logic/accelerator/ + handler/accelerator/  新增：加速源 API
internal/logic/selfupdate/ + handler/selfupdate/    新增：自更新 API
internal/handler/favicon/          新增：favicon 解析 API
internal/utiles/stopcontainer.go / restartcontainer.go  修改：登记守护静默窗口
```

前端（[libremk66/DockHamster-UI](https://github.com/libremk66/DockHamster-UI)）：
`src/components/AutoUpdate.jsx`（新增页面：白名单/通知/进度/旧镜像策略/容器异常告警开关）、`Containers.jsx`（列表化/搜索/开关/整组弹窗/进度子行/回滚入口/端口直达/本面板标识）、`Images.jsx`（列表化/搜索/快照分类/加速拉取入口）、`Migrate.jsx`（迁移页：体检/导出/导入）、`Accelerator.jsx`（加速源面板 + 加速拉取弹窗）、`SelfUpdate.jsx`（面板自更新）、`ContainerLogo.jsx`（图标：内置/自定义 logo → favicon 兜底）、`ProgressBar.jsx`、`Header.jsx`、`App.jsx`、`api/client.js`、`utils/webFavicon.js`、`utils/format.js`。

## 五、版本与发布机制

**版本号三处一致**：仓库根目录的 `version` 文件是唯一事实来源（如 `v1.3.0`），一次提交同时驱动三件事：

1. **镜像构建**：`build.yml` 构建并推送 `libremk66/dockhamster:latest` + `:v1.3.0`（多架构）
2. **GitHub Release**：`release.yml` 自动打 tag、创建 Release 并生成变更日志（发版零手工步骤）
3. **面板内的更新提示**：面板读取同一个 `version` 文件来判断是否有新版

**面板怎么知道有新版本**（双保险，任一命中即提示）：

- **版本号比较**：拉取仓库 main 分支的 `version` 文件（**多源自动兜底**：GitHub raw → jsDelivr CDN → 公共镜像站，每源 6 秒超时、10 分钟缓存、记住上次可用的源）——**无需任何代理配置**
- **镜像 digest 比对**：直接问镜像仓库"`libremk66/dockhamster:latest` 的 digest 变了没"（复用容器更新检测的同一套逻辑）——防止某次提交忘了改版本号，用户仍能收到提示

**更新方式**（Docker 部署）：

- **面板内一键自更新（推荐，v1.4.0 起）**：侧栏「有新版本」弹窗点「立即更新面板」——用接力容器完成替换，**失败自动回滚**，面板重启约 20 秒；容器页对 `dockhamster` 容器点「更新」同效（自动切换到接力流程）
- 或命令行：`docker compose pull && docker compose up -d`

## 六、与上游的关系

本项目为独立维护的社区增强版：**不再自动 rebase 上游**，但保留了完整的 git 历史与出处标注（AGPL 要求）；上游修复会按需 cherry-pick。

通用修复建议持续向上游提 PR；一旦合并，对应实现即可与上游对齐。

## 七、构建与发布

- 推送 `main` 分支 → GitHub Actions（`build.yml`）自动构建 amd64/arm64 并推送：
  `libremk66/dockhamster:latest`（同时打 `:vX.Y.Z` 版本 tag）
- 需要仓库 Secrets：`DOCKERHUB_USERNAME`、`DOCKERHUB_TOKEN`

## 八、许可

遵循 **AGPL-3.0**（与上游一致）。完整源码即本仓库（`main` 分支），AGPL §13 合规。版权归属：上游原作者 + 本项目贡献者。
