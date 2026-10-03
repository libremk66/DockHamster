package utiles

// 包内脚本/文档模板（占位符 __XXX__ 由 buildImportScript / buildReadmeTxt 替换）

const importScriptTemplate = `#!/bin/sh
# ═══════════════════════════════════════════════════════════════
#  DockHamster 迁移包 · 无面板导入脚本
#  包名：__PKG_NAME__
#  用途：在只有 Docker（没装 DockHamster）的机器上还原本包的容器
#  原则：只用标准 docker 命令；不删除任何东西；默认每一步都确认
#  用法：./import.sh --dry-run     只检查、不动手
#        ./import.sh               交互式导入
#        ./import.sh -y            跳过确认（自动化）
#        ./import.sh --auto-create-dirs   缺失的宿主目录自动创建（空目录）
# ═══════════════════════════════════════════════════════════════
set -eu

DRY_RUN=0; ASSUME_YES=0; AUTO_DIRS=0
for a in "$@"; do
  case "$a" in
    --dry-run) DRY_RUN=1 ;;
    -y|--yes) ASSUME_YES=1 ;;
    --auto-create-dirs) AUTO_DIRS=1 ;;
    *) echo "未知参数: $a"; exit 1 ;;
  esac
done

DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
cd "$DIR"
MANIFEST="manifest.json"
IMAGES_DIR="images"
COMPOSE_FILE="compose.yaml"

say()  { printf '%s\n' "$*"; }
step() { printf '\n\033[1;34m▶ %s\033[0m\n' "$*"; }
warn() { printf '\033[1;33m⚠ %s\033[0m\n' "$*"; }
die()  { printf '\033[1;31m✗ %s\033[0m\n' "$*"; exit 1; }

ask() {
  [ "$ASSUME_YES" = 1 ] && return 0
  printf '%s [y/N] ' "$1"; read -r ans
  case "$ans" in y|Y|yes|YES) return 0 ;; *) return 1 ;; esac
}

step "环境自检"
command -v docker >/dev/null 2>&1 || die "找不到 docker 命令，请先安装 Docker"
say "  ✓ docker: $(docker version --format '{{.Server.Version}}' 2>/dev/null || echo '守护进程未响应')"
[ -f "$MANIFEST" ] || die "缺少 $MANIFEST（请在解包后的目录里运行本脚本）"
[ -d "$IMAGES_DIR" ] || die "缺少 $IMAGES_DIR 目录"

COMPOSE_OK=1
docker compose version >/dev/null 2>&1 || COMPOSE_OK=0
[ "$COMPOSE_OK" = 1 ] && say "  ✓ docker compose 可用" || warn "docker compose 不可用（稍后只能手工起容器）"

NEED_KB=$(du -sk "$IMAGES_DIR" 2>/dev/null | awk '{print $1}')
AVAIL_KB=$(df -Pk "$DIR" | awk 'NR==2{print $4}')
say "  镜像文件占用: $((NEED_KB/1024)) MB ／ 当前分区可用: $((AVAIL_KB/1024)) MB"
if [ "$AVAIL_KB" -lt $((NEED_KB*2)) ]; then
  warn "可用空间不足镜像大小的 2 倍（load 后镜像还会占一份），请先清理磁盘"
  [ "$DRY_RUN" = 1 ] || ask "仍然继续？" || exit 1
fi

if [ -f checksums.txt ]; then
  step "校验镜像完整性（sha256sum -c）"
  if sha256sum -c checksums.txt >/dev/null 2>&1; then say "  ✓ 全部校验通过"
  else warn "校验失败！文件可能损坏，建议重新传输本包"; [ "$DRY_RUN" = 1 ] || ask "仍然继续？" || exit 1; fi
fi

step "导入镜像（docker load）"
for f in "$IMAGES_DIR"/*.tar "$IMAGES_DIR"/*.tar.gz; do
  [ -e "$f" ] || continue
  say "  → $(basename "$f")"
  if [ "$DRY_RUN" = 1 ]; then say "    (dry-run 跳过)"; continue; fi
  docker load -i "$f" || die "load 失败: $f"
done

step "卷路径检查"
if command -v python3 >/dev/null 2>&1; then
  python3 - "$MANIFEST" "$AUTO_DIRS" <<'PY' || true
import json, os, sys
m = json.load(open(sys.argv[1]))
auto = sys.argv[2] == "1"
missing = []
for c in m.get("containers", []):
    for v in (c.get("volumes") or []):
        src = v.get("source") or ""
        if v.get("type") == "bind" and src.startswith("/") and not os.path.exists(src):
            missing.append((c.get("name", "?"), src, v.get("target", "?")))
if missing:
    print("  以下宿主路径在目标机器上不存在（数据需自行同步）：")
    for n, s, t in missing:
        print("    - [%s] %s  → 容器内 %s" % (n, s, t))
    if auto:
        for _, s, _ in missing:
            try:
                os.makedirs(s, exist_ok=True)
                print("    ✓ 已创建空目录: %s（不含数据）" % s)
            except Exception as e:
                print("    ✗ 创建失败 %s: %s" % (s, e))
else:
    print("  ✓ 所有宿主路径均已存在")
PY
else
  warn "缺少 python3，跳过卷路径自动检查（请对照 README.txt 手工确认）"
fi

step "重建容器"
if [ "$COMPOSE_OK" = 1 ] && [ -f "$COMPOSE_FILE" ]; then
  say "  将执行: docker compose -f $COMPOSE_FILE up -d"
  if [ "$DRY_RUN" = 1 ]; then
    say "  (dry-run 跳过，下面是 compose 校验结果)"
    docker compose -f "$COMPOSE_FILE" config >/dev/null && say "  ✓ compose 文件语法正确"
  else
    ask "现在创建并启动这些容器？" || { warn "已跳过容器创建"; exit 0; }
    docker compose -f "$COMPOSE_FILE" up -d
    say ""
    say "  ✓ 完成。查看状态： docker compose -f $COMPOSE_FILE ps"
  fi
else
  warn "没有 compose，请手工执行（见 README.txt 场景 C）"
fi

step "后续手动步骤"
say "  1) 卷数据：本包不含数据，请按 README.txt 的路径清单自行同步"
say "  2) 装回面板（可选）：README.txt 末尾有一行 docker run 命令"
[ "$DRY_RUN" = 1 ] && say ""
[ "$DRY_RUN" = 1 ] && say "（本次为 dry-run，未做任何修改）"
exit 0
`

const readmeTemplate = `═══════════════════════════════════════════════════════════════
  DockHamster 迁移包 · 导入指引
  包名: __PKG_NAME__
  生成自: __HOST__ (__VERSION__)   时间: __CREATED_AT__
  内容: __IMAGE_COUNT__ 个镜像 + __CONTAINER_COUNT__ 个容器配方
═══════════════════════════════════════════════════════════════

【包里有什么】
  README.txt        本文件
  import.sh         无面板一键导入脚本（只用标准 docker 命令）
  compose.yaml      可直接使用的 compose 文件（含全部容器定义）
  manifest.json     机器可读清单（镜像摘要 / 容器配方 / 校验和）
  images/           镜像文件（docker save 出来的 tar）
  checksums.txt     校验和（可选验证：sha256sum -c checksums.txt）

┌─────────────────────────────────────────────────────────────┐
│ 场景 A：目标机器已经装了 DockHamster（最省事）              │
└─────────────────────────────────────────────────────────────┘
  1. 打开面板 → 侧栏「迁移」→「导入」
  2. 上传本包（.tar.gz）
  3. 按预检结果确认（冲突会自动提示，可改名/改端口/改卷路径）
  4. 点「执行导入」

┌─────────────────────────────────────────────────────────────┐
│ 场景 B：目标机器只有 Docker，还没装面板（换机迁移常遇到）    │
└─────────────────────────────────────────────────────────────┘
  # 1) 解包
  tar -xzf __PKG_NAME__.tar.gz
  cd __PKG_NAME__

  # 2) 先干跑一遍做检查：磁盘空间 / 卷路径 / 镜像校验
  ./import.sh --dry-run

  # 3) 确认无误后实际导入（load 镜像 → compose 起容器）
  ./import.sh

  说明:
   · import.sh 只用标准 docker 命令，不联网、不需要面板
   · 不会删除任何东西；不存在的宿主目录只报警告，不自动创建
     （如需自动创建空目录：./import.sh --auto-create-dirs）
   · 起容器前会打印将要执行的命令并等你确认（-y 可跳过确认）

┌─────────────────────────────────────────────────────────────┐
│ 场景 C：想手工导入 / 用别的工具（Portainer、1Panel…）        │
└─────────────────────────────────────────────────────────────┘
  1) 导入镜像（逐个 load，load 后镜像会保留原 tag）:
       for f in images/*.tar.gz images/*.tar; do [ -e "$f" ] && docker load -i "$f"; done

  2) 查看要重建什么:
       cat compose.yaml

  3) 起容器，任选其一:
       · 命令行:   docker compose -f compose.yaml up -d
       · Portainer: Stacks → Add stack → 粘贴 compose.yaml 内容 → Deploy
       · 其它面板: 把 compose.yaml 导入或粘贴进「编排/Stack」功能

【⚠️ 本包不含什么】
  卷数据（容器挂载的目录内容）不在包里。以下是本包容器用到的宿主路径，
  请在目标机器上确认这些路径存在，并自行同步数据（rsync/scp/网盘均可）：

__VOLUME_LIST__
  同步示例:
    rsync -avP /源路径/ 新机器:/目标路径/

【镜像清单】
__IMAGE_LIST__
  （"公共可得"的镜像目标机可直接拉取；"本地构建"必须靠本包搬运）

【导入后想装回面板】
  docker run -d --name dockhamster --restart always \
    -p 12712:12712 -v /var/run/docker.sock:/var/run/docker.sock \
    -v /你的数据路径/dockhamster:/data \
    -e secretKey=你的密钥 \
    libremk66/dockhamster:latest
  （也可用项目 README 里的 compose 方式）
`
