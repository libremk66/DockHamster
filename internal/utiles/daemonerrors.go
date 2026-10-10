package utiles

import (
	"fmt"
	"regexp"
	"strings"
)

// FriendlyDaemonError 把 Docker daemon / SDK 的原始英文报错翻译成"人话 + 处置建议"，
// 并把原文附在括号里（排障要用，别丢）。匹配不到时原样返回。
//
// 覆盖高频场景：删镜像被占用 / 有子镜像、创建容器失败（端口 / 名字 / 挂载 / 老 daemon）、
// 连不上 Docker、拉取限流与网络问题、容器启停常见错误。见 daemonErrRules。
//
// 约定：错误文案格式 =「中文说明（原始错误：<截断后的原文>）」，
// 用法：在把 err 交给用户之前套一层，例如 resp.Msg = utiles.FriendlyDaemonError(err).Error()
func FriendlyDaemonError(err error) error {
	if err == nil {
		return nil
	}
	raw := err.Error()
	low := strings.ToLower(raw)
	for _, r := range daemonErrRules {
		if !strings.Contains(low, r.substr) {
			continue
		}
		if r.substr2 != "" && !strings.Contains(low, r.substr2) {
			continue
		}
		if r.keepRaw {
			return fmt.Errorf("%s（原始错误：%s）", r.msg(raw), oneLine(raw))
		}
		return fmt.Errorf("%s", r.msg(raw))
	}
	return err
}

type daemonErrRule struct {
	substr  string // 小写关键词（原文转小写后匹配）
	substr2 string // 可选：第二个必须同时出现的关键词
	msg     func(raw string) string
	// keepRaw=false 时不再附原文（译文本身已完整，或原文过长无信息量）
	keepRaw bool
}

// pick 从原文里抓第一个捕获组，用于把容器 ID / 端口 / 路径等带进译文
func pick(raw, pattern string) string {
	m := regexp.MustCompile(pattern).FindStringSubmatch(raw)
	if len(m) > 1 {
		return m[1]
	}
	return ""
}

// wrap 把取值包成「（xxx）」；取不到就返回空串
func wrap(v, prefix string) string {
	if v == "" {
		return ""
	}
	return "（" + prefix + v + "）"
}

var daemonErrRules = []daemonErrRule{
	// ── 删除镜像 ───────────────────────────────────────────────
	{
		substr: "image is being used by running container", keepRaw: true,
		msg: func(raw string) string {
			return "镜像正在被容器" + wrap(pick(raw, `(?i)running container ([0-9a-f]{8,})`), "") +
				"使用，无法删除；请先停止该容器，或到「容器」页把它删除"
		},
	},
	{
		substr: "image is being used by stopped container", keepRaw: true,
		msg: func(raw string) string {
			return "镜像正在被已停止的容器" + wrap(pick(raw, `(?i)stopped container ([0-9a-f]{8,})`), "") +
				"使用；可在「镜像」页用「强删」把它一并处理"
		},
	},
	{
		substr: "image has dependent child images", keepRaw: false,
		msg: func(string) string {
			return "该镜像被其他镜像作为基础镜像依赖（本地构建的父层），无法直接删除；删除依赖它的镜像后即可清理"
		},
	},
	{
		substr: "no such image", keepRaw: false,
		msg: func(string) string {
			return "镜像不存在（可能已被删除，刷新列表即可）"
		},
	},

	// ── 连接 / 权限 ────────────────────────────────────────────
	{
		substr: "cannot connect to the docker daemon", keepRaw: false,
		msg: func(string) string {
			return "面板连不上 Docker；请确认面板容器已挂载 /var/run/docker.sock，且 Docker 服务正在运行"
		},
	},
	{
		substr: "docker.sock", substr2: "permission denied", keepRaw: false,
		msg: func(string) string {
			return "没有访问 Docker socket 的权限；请确认面板容器已挂载 /var/run/docker.sock（或给足权限）"
		},
	},

	// ── 更新 / 创建容器 ────────────────────────────────────────
	{
		substr: "port is already allocated", keepRaw: true,
		msg: func(raw string) string {
			port := pick(raw, `(?i)(?:bind for [^:]*:|0\.0\.0\.0:)(\d+)`)
			return "宿主端口" + wrap(port, "") + "已被占用，无法创建容器；请先停用占用该端口的容器，或在更新时修改端口映射"
		},
	},
	{
		substr: "container name", substr2: "is already in use", keepRaw: true,
		msg: func(raw string) string {
			name := pick(raw, `(?i)container name "?/?([^"]+)"? is already in use`)
			return "已有同名容器" + wrap(name, "") + "存在，无法创建；请先删除或改名它"
		},
	},
	{
		substr: "name is already in use by container", keepRaw: true,
		msg: func(raw string) string {
			return "该容器名已被占用；请换一个名字，或先删除同名容器"
		},
	},
	{
		substr: "bind source path does not exist", keepRaw: true,
		msg: func(raw string) string {
			p := pick(raw, `(?i)bind source path does not exist: ([^ "]+)`)
			return "挂载的宿主路径" + wrap(p, "") + "不存在；请先创建该目录（或修正挂载配置）后重试"
		},
	},
	{
		substr: "requires api version", keepRaw: true,
		msg: func(string) string {
			return "宿主机的 Docker 版本过老，当前操作需要更新的 API 版本；建议升级 Docker（群晖用户升级 DSM / Container Manager）"
		},
	},
	{
		substr: "no space left on device", keepRaw: false,
		msg: func(string) string {
			return "磁盘空间不足，操作无法完成；请先清理镜像/快照或扩容后重试"
		},
	},
	{
		substr: "exec format error", keepRaw: false,
		msg: func(string) string {
			return "镜像架构与宿主机不匹配（例如 arm64 镜像跑在 amd64 上），无法启动"
		},
	},

	// ── 拉取 / 网络 ────────────────────────────────────────────
	{
		substr: "toomanyrequests", keepRaw: true,
		msg: func(string) string {
			return "Docker Hub 匿名拉取已达上限（临时限流）；开启「更新时自动走加速源」或登录 Hub 账号可绕开"
		},
	},
	{
		substr: "pull rate limit", keepRaw: true,
		msg: func(string) string {
			return "Docker Hub 匿名拉取已达上限（临时限流）；开启「更新时自动走加速源」或登录 Hub 账号可绕开"
		},
	},
	{
		substr: "denied: requested access to the resource is denied", keepRaw: false,
		msg: func(string) string {
			return "镜像仓库拒绝访问（私有镜像或未登录）；请先登录该仓库，或确认镜像可公开拉取"
		},
	},
	{
		substr: "no matching manifest for", keepRaw: true,
		msg: func(string) string {
			return "仓库里没有匹配当前 CPU 架构的镜像（例如 amd64 宿主机拉取仅 arm64 的镜像）"
		},
	},
	{
		substr: "x509", keepRaw: true,
		msg: func(string) string {
			return "HTTPS 证书校验失败（常见于代理/中间人劫持或时钟不准）；请检查面板容器的代理与证书配置"
		},
	},
	{
		substr: "tls handshake timeout", keepRaw: false,
		msg: func(string) string {
			return "连接镜像仓库超时（TLS 握手未完成）；请检查网络与代理后重试"
		},
	},
	{
		substr: "no such host", keepRaw: false,
		msg: func(string) string {
			return "域名解析失败；请检查面板容器的 DNS / 代理配置"
		},
	},
	{
		substr: "i/o timeout", keepRaw: false,
		msg: func(string) string {
			return "网络超时；请检查网络或稍后重试"
		},
	},
	{
		substr: "context deadline exceeded", keepRaw: false,
		msg: func(string) string {
			return "操作超时（大镜像或网络较慢时可能发生）；请稍后重试"
		},
	},
	// 仓库里没有这个镜像（与 FriendlyPullError 同义；这里是不带镜像名的兜底版本）
	{
		substr: "manifest unknown", keepRaw: true,
		msg: func(string) string {
			return "镜像不在可访问的仓库中（可能是本地构建、私有镜像或名字不存在）"
		},
	},
	{
		substr: "repository does not exist", keepRaw: true,
		msg: func(string) string {
			return "镜像仓库不存在（可能是本地构建、私有镜像或名字拼错）"
		},
	},
	{
		substr: "pull access denied", keepRaw: true,
		msg: func(string) string {
			return "没有拉取权限（可能是本地构建的镜像、私有镜像或名字不存在）"
		},
	},

	// ── 容器启停 / 改名 ────────────────────────────────────────
	{
		substr: "no such container", keepRaw: false,
		msg: func(string) string {
			return "容器不存在（可能已被删除，刷新列表即可）"
		},
	},
	{
		substr: "container is not running", keepRaw: false,
		msg: func(string) string {
			return "容器当前未在运行"
		},
	},
	{
		substr: "is already stopped", keepRaw: false,
		msg: func(string) string {
			return "容器已经是停止状态"
		},
	},
	{
		substr: "is already running", keepRaw: false,
		msg: func(string) string {
			return "容器已经在运行中"
		},
	},
	{
		substr: "already in progress", keepRaw: false,
		msg: func(string) string {
			return "该容器上已有操作正在进行（删除/停止等），请稍候重试"
		},
	},
}
