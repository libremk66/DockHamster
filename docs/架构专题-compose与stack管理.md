# 架构专题：compose / stack 管理该怎么进 DockHamster

> 状态：**待决策**（2026-10-09 立项，用户要求"不急，先把架构想清楚"）
> 触发事件：社区 PR [DockHamster-UI#1](https://github.com/libremk66/DockHamster-UI/pull/1)（compose 页）＋用户提出「可否融合 Portainer API 做 stack 级管理更新」。
> 本文只记录**问题、事实、选项与待决项**，不预设结论。

---

## 一、问题是什么

DockHamster 目前对 compose 的处理是"**容器视角**"：把 compose 容器当普通容器更新（走 API 重建），或在能读到 compose 文件时走 `docker compose up --no-deps <service>` 单服务重建（v1.4.1 已上线，含文件不可达时的回退）。

社区 PR 带来了"**项目视角**"的界面（项目分组 / 单服务更新 / 重建 / 日志 / 原文）。但它的前提是**面板能读到 compose 文件**——对"用 Portainer 管理 stack 的用户"（本项目的重度用户画像）覆盖很差。

用户提出的方向：**是否融合 Portainer API，基于 stack 做 compose 管理和更新**。

---

## 二、事实盘点（全部实测）

### 2.1 当前已具备的能力

| 能力 | 状态 | 说明 |
|---|---|---|
| 识别 compose 容器 | ✅ v1.4.1 | 解析 `com.docker.compose.*` labels（project/service/config_files/working_dir） |
| 单服务 compose 重建 | ✅ v1.4.1 | 需文件可达；不可达时**回退 API 重建**（进度里显式提示） |
| compose 文件读取 API | ✅ v1.4.1 | `GET /api/container/:id/composefile`（不可达时报明确错误） |
| 容器日志 API | ✅ v1.4.1 | `GET /api/container/:id/logs`（最近 200 行） |
| 更新检测/自动更新/通知/任务页 | ✅ | 都是"容器级"的，与 compose/stack 概念无耦合 |

### 2.2 文件从哪来（决定了"能不能读"）

| 用户类型 | compose 文件位置 | 面板直读？ |
|---|---|---|
| 宿主机直接 `docker compose` | 宿主机任意目录（文件里记的路径） | ✅ 挂目录即可 |
| **Portainer stack** | **Portainer 容器的 `/data/compose/<stackid>/docker-compose.yml`**（我们这台：bind 挂载在 `/home/hxsy/docker1/portainer`） | ⚠️ 需把 Portainer 数据目录挂进面板；**且拿不到 Portainer 的 env 变量覆盖**（存在 Portainer 数据库，不在文件里）→ 外部 `docker compose up` 会丢变量 |
| 其他面板/工具生成 | 各自内部目录 | 视情况 |

### 2.3 Portainer API 能提供什么（实测 Portainer 2.45.2）

| 端点 | 用途 |
|---|---|
| `GET /api/stacks?endpointId=N` | 列出全部 stack（本机 **84 个**） |
| `GET /api/stacks/{id}?endpointId=N` | 详情：`Name / Type / Env[] / Status / CreationDate / UpdateDate / GitConfig / AutoUpdate` |
| `GET /api/stacks/{id}/file?endpointId=N` | **compose 文件原文** |
| `PUT /api/stacks/{id}?endpointId=N` | **更新 stack**：body `{stackFileContent, env, prune, pullImage}` —— 传 `pullImage:true` 即"拉新镜像并重部署" |
| `POST /api/stacks/{id}/start｜stop` | 整栈起停 |
| 认证 | `X-API-Key`（面板可让用户填 URL + API Key） |

**关键点**：
1. `Env` 在 stack 详情里可读、且更新时可原样回传 → **Portainer env 变量不会丢**（这正好补上 2.2 的坑）；
2. `PUT` 是**同步调用**：只能拿到"开始/结束/失败"，**没有逐层进度**（不像我们自己拉镜像能显示 字节/速度）；
3. Portainer 的 stack 更新 = `docker compose pull + up -d`，**只重建有变化的服务**（compose 语义），不打扰同栈其它服务；
4. **Portainer 自身没有"镜像有新版 → 自动更新 stack"的能力**（只有 git 型 stack 的轮询更新，且本机 84 个 stack 全部未用 GitConfig）→ 与 DockHamster 的"更新检测 + 自动更新 + 通知"打通后，是**Portainer 没有的东西** ← 这是 Portainer 路线最大的产品价值点。

---

## 三、三条路线

### 路线 A：文件挂载 + `docker compose` CLI（= 社区 PR 的路线）
- **做法**：面板挂载 compose 目录；更新/重建走 compose CLI；原文直读文件。
- ✅ 不依赖任何第三方面板；真实 compose 语义；可单服务重建。
- ❌ 对 Portainer 用户要挂 Portainer 数据目录，且**丢 env 变量**（除非用户不用该功能）；对"没挂目录"的用户功能半残（原文不可用、重建误导）。
- 📌 已经实现了一半（v1.4.1 的 compose 通道）。

### 路线 B：Portainer API 集成
- **做法**：面板设置里填 Portainer URL + API Key → 列 stack / 看原文 / 更新 stack（`pullImage`）/ 起停；把 stack 更新接入**自动更新白名单与通知**。
- ✅ 零挂载、零路径问题、env 安全；对 Portainer 用户（本机 84 stack）**心智完全一致**；能做出"Portainer 没有的 stack 自动更新"。
- ❌ 依赖用户装 Portainer 并给 API Key；**进度只有开始/结束**（同步调用）；单服务粒度的"重建/更新"在 Portainer 语义里是整栈操作；对"只想手动"的用户是重复能力（但集成进面板=统一入口+记录+通知）。

### 路线 C：混合（自动探测）
- 配置了 Portainer → 走 B；能读到文件 → 走 A；都没有 → 现有容器级更新。
- ✅ 覆盖全部用户；❌ 三套路径的语义/进度/错误都要分别维护，复杂度最高。可先做 A+B，探测逻辑后置。

---

## 四、待决问题（给决策用）

1. **受众**：DockHamster 的 compose 能力，主要服务"宿主机 compose 用户"还是"Portainer 用户"？（本机实装 84 个 stack 的现实 → Portainer 权重不低）
2. **产品定位**：compose/stack 页是"**操作台**"还是"**只读视图**（分组+日志+原文）+ 把更新交给现有自动更新流程"？
3. **是否接受依赖第三方（Portainer）**：核心项目引入对另一面板的集成，边界怎么描述（用于 README/定位文案）？
4. **进度体验的取舍**：Portainer 路线拿不到字节级进度——接受"整栈更新中…"的粗略反馈，还是坚持"自己拉镜像+compose"以保留进度？（也可混合：拉取我们自己做、重部署交给 Portainer）
5. **与现有 compose 通道的关系**：文件可达时优先谁？（同一台机器两种路径并存会导致行为不一致）
6. **社区 PR 的处置**（见第五节）。

## 五、社区 PR 的处置记录（2026-10-09）

- **compose 页部分：暂缓**。理由：其价值假设（面板能读 compose 文件）对本项目主力用户画像（Portainer）覆盖差；架构未定前合并会造成"半可用功能+既定交互"。
- **镜像页部分（标签数徽标 / 被镜像依赖标记 / 清理跳过不可删项）：独立且与已合并的后端能力对齐，计划单独合入**（建议由作者拆分为独立 PR，或经其同意后我们摘取该提交并保留署名）。
- 回复要点：感谢贡献 + 说明"compose 交互我们想先定架构（正在评估 Portainer API 在内的路线）" + 请拆分镜像页部分 + 承诺架构定案后回到该 PR。

## 六、参考实现（PR #1 的代码价值，无论最终是否采用）

- 项目分组 / 服务行交互 / 内联进度的前端实现；
- 镜像页 `hasChildren`/`tags` 的用法；
- 其"更新 vs 重建（skipPull）"的语义区分——**这条语义在 Portainer 路线里要重新设计**（Portainer 没有单服务重建，对应的是整栈 `up -d` + 可选 `--force-recreate`）。
