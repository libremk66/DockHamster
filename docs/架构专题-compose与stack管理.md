# 架构专题：compose / stack 管理该怎么进 DockHamster

> 状态：**方向已定，待排期**（2026-10-09 立项，同日讨论后收敛）
> 触发事件：社区 PR [DockHamster-UI#1](https://github.com/libremk66/DockHamster-UI/pull/1)（compose 页）＋用户提出「可否融合 Portainer API 做 stack 级管理更新」。
>
> **结论先行**（详见第五、六节）：
> 1. 走「**文件优先**」路线，并给还没挂目录的用户**挂载引导**；
> 2. 能力做**分层**（T0 零配置 → T1 挂目录 → T2 Portainer API），不是三选一；
> 3. **Portainer API 从"唯一正解"降级为可选增强**——实测本机 84 个 stack 全部不依赖 Portainer 变量，文件即完整事实来源（3.3）；
> 4. 定位是 compose 的「**更新管家**」，不是编辑器——不与 Dockhand / Portainer / Dockge 正面竞争。

---

## 一、问题是什么

DockHamster 目前对 compose 的处理是"**容器视角**"：把 compose 容器当普通容器更新（API 重建），或在能读到 compose 文件时走 `docker compose up -d --no-deps <service>` 单服务重建（v1.4.1 已上线，含文件不可达时的回退提示）。

社区 PR 带来了"**项目视角**"的界面（项目分组 / 单服务更新 / 重建 / 日志 / 原文），但前提是**面板能读到 compose 文件**——它没有回答"怎么让文件可达"，装上一半项目是灰的。

---

## 二、用户画像：compose 文件放哪，分三派

| 派别 | 典型形态 | 面板要拿到文件，需要 |
|---|---|---|
| **① 统一目录派**（手写党 / Dockge 用户） | `~/docker/<应用>/docker-compose.yml`（最常见）；`/opt/stacks/<项目>/compose.yaml`（Dockge 的强制约定）；`/opt/docker/*.yml` 扁平单文件 + 同目录 `.env` | 挂**一个**根目录（同路径挂载，保证相对路径不错位） |
| **② 面板托管派**（Portainer / 1Panel / 群晖 / UGREEN…） | 文件在面板自己的数据卷里：Portainer = `<数据卷>/compose/<id>/docker-compose.yml`；变量可能存面板数据库 | 挂该面板的数据目录；变量问题见 3.3 |
| **③ 散落派** | 一个应用一个地方；或压根没有 compose 文件（纯 `docker run` / 面板创建） | 逐个挂载不现实 → 只能走容器级更新 |

关键事实：**容器 label 里就写着"这个容器从哪个 compose 文件来的"**（`com.docker.compose.project.config_files` / `working_dir`），所以"用户属于哪一派、文件在哪"是**可以探测的**，不需要用户自己描述。

---

## 三、事实盘点（全部实测）

### 3.1 当前已具备的能力（v1.4.1）

| 能力 | 说明 |
|---|---|
| 识别 compose 容器 | 解析 `com.docker.compose.*` labels（project / service / config_files / working_dir） |
| 单服务 compose 重建 | 文件可达时走 compose CLI；不可达时回退 API 重建并在进度里显式提示 |
| compose 原文 / 容器日志 API | `GET /api/container/:id/composefile`、`.../logs`（不可达时明确报错） |
| 更新检测 / 自动更新 / 通知 / 任务页 | 均为容器级，与 compose 概念无耦合 |

### 3.2 文件在哪、能不能自动找到

| 用户类型 | 文件实际位置 | 面板直读 |
|---|---|---|
| ① 统一目录派 | label 里的宿主机路径（挂同路径即可读） | ✅ 挂一次全拿到 |
| ② Portainer | label 写的是 **Portainer 容器内视角** `/data/compose/<id>/…`；宿主上在 `<Portainer 数据卷>/compose/<id>/`（本机 = `/home/hxsy/docker1/portainer/compose/<id>/`） | ⚠️ 需要**映射**，但映射可自动推导 |
| ③ 散落 / 无文件 | — | ❌ 走容器级更新 |

**自动推导映射（新增发现）**：面板 inspect 一下 `portainer` 容器，就能看到 `/data` 挂载到宿主哪个目录 → 把 `/data/compose/<id>/…` 换算成真实路径。**用户不需要手填任何路径**，我们已有的 `COMPOSE_ALT_ROOTS` 兜底机制正好可以接这个推导结果。

### 3.3 ⭐ Portainer 的 env 问题，在本机实测为"不存在"

原本认为 Portainer 路线（B）不可替代的原因是：**变量存在 Portainer 数据库、文件里是 `${VAR}`，外部 `docker compose up` 会把变量解析成空值**。实测本机（Portainer 2.45.2，84 个 stack）：

| 检查项 | 结果 |
|---|---|
| 栈目录下的 `.env` 文件 | **0 个**（每个栈目录只有单独一个 `docker-compose.yml`） |
| `Env[]` 非空的 stack | **0 / 84** |
| compose 文件里含 `${变量}` 的 | 2 个，**且都是注释里的示例文字** |

→ 对本机这类用法，**文件就是完整的事实来源**，直接拿文件跑 `docker compose up -d --no-deps` 就是正确语义，不需要问 Portainer 要任何东西。

> 但**不能推广到所有 Portainer 用户**：在 Portainer UI 里填了环境变量的栈，`Env[]` 是有值的、文件里是占位符。→ 处理方式见 T1 的"变量体检"。

### 3.4 Portainer API 仍然能提供什么（保留价值）

| 端点 | 用途 |
|---|---|
| `GET /api/stacks?endpointId=N` | 列出全部 stack（本机 84 个） |
| `GET /api/stacks/{id}` | 详情：`Name / Type / Env[] / Status / GitConfig / AutoUpdate` |
| `GET /api/stacks/{id}/file` | compose 文件原文 |
| `PUT /api/stacks/{id}` | 更新 stack：`{stackFileContent, env, prune, pullImage}`，`pullImage:true` = 拉新镜像并重部署 |
| `POST /api/stacks/{id}/start｜stop` | 整栈起停 |

**它不可替代的场景只剩三个**：
1. 栈**真的用了** `Env[]`（变量在数据库）→ 只有 API 能保真更新；
2. **git 型 stack** 的更新/同步；
3. 想让 **Portainer 自己的状态**保持准确（外部改了文件，Portainer 不知情）。

其它情况它都是"更绕的一条路"：`PUT` 是同步调用、**没有逐层进度**（不像我们自己拉镜像能显示字节/速度）。

---

## 四、案例研究：Dockhand（同赛道新秀，2026-10 查阅源码）

[Dockhand](https://github.com/Finsys/dockhand)（~6.6k star，TypeScript/SvelteKit，Wolfi 基础镜像）在 compose 上的做法——**"扫描 + 接管 + 原地编辑"**：

- **文件永远留在用户目录**。源码注释原话：*"Discovered stacks are editable — compose and .env files are modified in their original location"*；
- **扫描**用户配置的外部路径：识别 `compose.yaml / compose.yml / docker-compose.yml / docker-compose.yaml`，要求含 `services:`，最大递归 5 层，可跳过目录；
- **接管**（`POST /api/stacks/adopt`）是**用户点确认**才纳入，不是自动收编；容器可带 `dockhand.adopt=false` 退出；
- 栈名优先取 compose 顶层 `name:`（与 Docker 的 `com.docker.compose.project` 对齐），否则用目录名；
- 同目录 `.env` 一并识别、原位置读写；新建的栈写入每环境配置的 stacks dir；
- 元数据进 SQLite/PG，但**文件是事实来源**。

**可借鉴**：扫描发现 + 用户确认接管 + 原地读写的整套交互；"用顶层 `name:` 当栈名"这类与 Docker 语义对齐的细节；opt-out label 的礼貌设计。

**⚠️ 禁区**：Dockhand 是 **BSL 1.1**（源码可见但**不是开源**，且有商用限制），与本项目 AGPL-3.0 不兼容——**只借鉴设计思路，一行代码都不能搬**。同类禁区：任何非 OSI 许可的面板代码。

**对我们的启示**：扫目录比只靠 label 更全（能发现"容器已删、文件还在"的栈），两者互补：label 管"正在用的"，扫描管"躺在盘上的"。

---

## 五、方案：能力分层（渐进增强）

| 层级 | 前提 | 能做什么 |
|---|---|---|
| **T0 零配置** | 什么都不配 | 按 label 分组显示**项目 / 服务视图**（不需要任何文件）；更新走现有 API 重建；对每个栈显示一行"挂载 X 目录可获得 compose 原生更新" |
| **T1 文件通道** | 面板能读到 compose 文件（统一目录派挂根目录；Portainer 派挂数据目录，映射自动推导） | 原文查看、`up -d --no-deps` 单服务重建（无配置漂移）、compose CLI 日志；**变量体检**：文件引用 `${X}` 而环境无 X → 明确警告"此栈依赖面板变量，外部更新会丢值"，而不是默默跑坏 |
| **T2 Portainer API**（可选） | 用户填 Portainer URL + API Key | 上述三个不可替代场景（Env[] 保真 / git stack / 保持 Portainer 状态一致）；**进度只有开始/结束** |

### ⭐ T0→T1 的杀手锏：挂载引导

社区 PR 那版最大的毛病是"装上一半项目是灰的，用户不知道要干什么"。我们要把这个前置解决：

1. 扫描所有 compose 容器的 label → 汇总出**需要挂载的最小目录集合**；
2. 直接给出可复制的 compose 覆盖片段（如 `- /opt/docker:/opt/docker`、`- /home/hxsy/docker1/portainer:/home/hxsy/docker1/portainer`）；
3. 挂载后逐项目显示状态：**✅ 可读 / ⚠️ 需挂载 / 🅿️ Portainer 托管（含变量警告）**；
4. 未来可补"目录扫描"（学 Dockhand）覆盖散落派与"文件还在、容器已删"的情况。

---

## 六、产品定位：做 compose 的"更新管家"，不做编辑器

Dockhand / Portainer / Dockge 都在做"**完整 compose 管理**"（编辑器、栈图、GitOps、密钥注入…）——这条路我们**不跟**：打不过，也不该打。

我们的差异点在"更新"这条主线上：

> 发现栈 → 每个服务像容器一样被**检测更新** → 更新时走 compose 语义 → 结果进**任务历史**、发**通知**、失败可**回滚**（健康校验）

即"**让 compose 栈进入自动更新链路**"——这是 Portainer（无镜像更新能力）和 Dockge（纯手动）都没有的。README/文档对外描述时按这个口径写。

---

## 七、待决问题

1. **排期**：T0（项目视图 + 挂载引导文案）成本最低、收益直观，是否作为下一步？T1 紧随其后？
2. **挂载引导的形态**：只在页面提示，还是提供一个"复制这段到 compose"的一键片段（需要面板知道自己的部署方式）？
3. **T2 是否真的要做**：若目标用户里"用 Portainer 变量"的比例极低，可以长期不做，只保留文档说明。
4. **目录扫描要不要做**（学 Dockhand 的扫描接管）：它引入"面板配置扫描根目录"的新概念，需权衡复杂度。
5. **社区 PR 的处置**（见第八节）。

## 八、社区 PR 的处置记录（2026-10-09）

- **compose 页部分：暂缓**。理由：其价值假设（面板能读 compose 文件）覆盖不全，且缺少"怎么让文件可达"的引导；架构定案前合并会造成"半可用功能+既定交互"。
- **镜像页部分（标签数徽标 / 被镜像依赖标记 / 清理跳过不可删项）：独立且与已合并的后端能力对齐，计划单独合入**（建议由作者拆分为独立 PR，或经其同意后我们摘取该提交并保留署名）。
- 回复要点：感谢贡献 + 说明"compose 交互我们想先定架构（正在评估 Portainer API 在内的路线）" + 请拆分镜像页部分 + 承诺架构定案后回到该 PR。

## 九、参考实现与借鉴清单

- **PR #1**：项目分组 / 服务行交互 / 内联进度的前端实现；镜像页 `hasChildren`/`tags` 用法；"更新 vs 重建（skipPull）"语义——该语义在 Portainer 路线里需重新设计（Portainer 没有单服务重建，对应整栈 `up -d` + 可选 `--force-recreate`）。
- **Dockhand**（只借鉴思路，**代码禁抄**，BSL 1.1）：扫描发现 → 用户确认接管 → 原地读写；顶层 `name:` 定栈名；`adopt=false` opt-out label。
- **Dockge**：`/opt/stacks` 单根目录约定（统一目录派的现实依据）。
