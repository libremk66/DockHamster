package utiles

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/libremk66/DockHamster/internal/svc"
	"github.com/zeromicro/go-zero/core/logx"
)

// composeExecTimeout 单个 compose 操作的超时（拉镜像可能较慢）
const composeExecTimeout = 10 * time.Minute

// composeBinary 探测面板运行环境内可用的 compose 命令；
// 优先 compose plugin（docker compose），退回独立二进制（docker-compose）
func composeBinary() (bin string, subcmd []string, err error) {
	candidates := []struct {
		bin    string
		subcmd []string
	}{
		{"docker", []string{"compose"}},
		{"docker-compose", nil},
	}
	for _, c := range candidates {
		path, lookupErr := exec.LookPath(c.bin)
		if lookupErr != nil {
			continue
		}
		// "docker compose version" 验证 plugin 真实可用（而非只存在 docker 二进制）
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		out, verErr := exec.CommandContext(ctx, path, append(c.subcmd, "version")...).CombinedOutput()
		cancel()
		if verErr != nil {
			continue
		}
		logx.Infof("compose 执行器: %s %s (%s)", c.bin, strings.Join(c.subcmd, " "),
			strings.TrimSpace(string(out)))
		return path, c.subcmd, nil
	}
	return "", nil, fmt.Errorf("面板容器内未找到 docker compose / docker-compose")
}

// ResolveConfigFiles 将 compose 文件路径解析为面板容器内可达的路径。
// altRootPrefixes 备用根前缀：同一数据卷在宿主上可能有多个挂载视角，
// 面板容器内只有其中一种可达。权威来源为环境变量 COMPOSE_ALT_ROOTS
// （逗号分隔前缀）；未配置时不启用兜底，路径不可达直接报错。
func altRootPrefixes() []string {
	v := os.Getenv("COMPOSE_ALT_ROOTS")
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	roots := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			roots = append(roots, p)
		}
	}
	return roots
}

// ResolveConfigFiles 将 compose 文件路径解析为面板容器内可达的路径。
// labels 记录的是容器创建时宿主机视角的路径，面板容器内该路径不一定一致。
// 策略：原路径可达则原样使用；否则按备用根前缀逐一探测替换，全部不可达才报错。
func ResolveConfigFiles(paths []string) ([]string, error) {
	roots := altRootPrefixes()
	resolved := make([]string, len(paths))
	for i, p := range paths {
		resolved[i] = p
		if _, err := os.Stat(p); err == nil {
			continue
		}
		alt := ""
		for _, root := range roots {
			candidate := filepath.Join(root, p)
			if _, err := os.Stat(candidate); err == nil {
				alt = candidate
				break
			}
		}
		if alt == "" {
			return nil, fmt.Errorf("compose 文件不可达: %s（面板容器需挂载该路径才能执行 compose 更新）", p)
		}
		logx.Infof("compose 文件路径解析: %s -> %s", p, alt)
		resolved[i] = alt
	}
	return resolved, nil
}

// composeUpService 对指定 compose 项目执行单 service 重建：
// docker compose -f <files...> -p <project> up -d --force-recreate --no-deps <service>
// 只影响目标 service，不触碰同项目其他容器；--no-deps 避免连带重启依赖服务。
// 前置条件：compose 文件路径在面板容器内可达（部署时需挂载，见 README）。
func composeUpService(meta ComposeMeta, force bool) error {
	bin, subcmd, err := composeBinary()
	if err != nil {
		return err
	}
	args := append([]string{}, subcmd...)
	files, err := ResolveConfigFiles(meta.ConfigFiles)
	if err != nil {
		return err
	}
	for _, f := range files {
		args = append(args, "-f", f)
	}
	if meta.WorkingDir != "" {
		args = append(args, "--project-directory", meta.WorkingDir)
	}
	args = append(args, "-p", meta.Project, "up", "-d", "--no-deps")
	if force {
		args = append(args, "--force-recreate")
	}
	args = append(args, meta.Service)

	ctx, cancel := context.WithTimeout(context.Background(), composeExecTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	detail := strings.TrimSpace(string(out))
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("compose up 超时（%s）", composeExecTimeout)
		}
		return fmt.Errorf("compose up 失败: %w\n%s", err, detail)
	}
	logx.Infof("compose up 完成 %s: %s", meta.UpdateRef(), detail)
	return nil
}

// findComposeContainerID 按 compose labels 在容器列表中定位当前容器 ID
func findComposeContainerID(serviceContext *svc.ServiceContext, meta ComposeMeta) (string, error) {
	list, err := serviceContext.DockerClient.ContainerList(context.Background(), container.ListOptions{All: true})
	if err != nil {
		return "", fmt.Errorf("list containers: %w", err)
	}
	for _, c := range list {
		if c.Labels[labelComposeProject] == meta.Project && c.Labels[labelComposeService] == meta.Service {
			return c.ID, nil
		}
	}
	return "", fmt.Errorf("未找到 compose 容器 %s", meta.UpdateRef())
}

// UpdateContainerViaCompose 走 compose 通道更新单个容器。
// skipPull=false：拉新镜像 → compose up 强制重建；skipPull=true：跳过拉取，
// 仅按 compose 当前配置强制重建（适用改了 compose.yml 让改动生效）。
// 失败时返回错误；由调用方决定展示（不自动回退 API 重建，避免 compose 配置漂移）。
func UpdateContainerViaCompose(serviceContext *svc.ServiceContext, meta ComposeMeta, imageNameAndTag string, skipPull bool, taskID string) error {
	if !meta.IsManaged {
		return fmt.Errorf("容器不属于任何 compose 项目")
	}
	setMsg := func(pct int, msg string, done bool) {
		serviceContext.UpdateProgress(taskID, svc.TaskProgress{
			TaskID:     taskID,
			Percentage: pct,
			Name:       meta.UpdateRef(),
			Message:    msg,
			DetailMsg:  msg,
			IsDone:     done,
		})
	}
	fail := func(pct int, err error) error {
		setMsg(pct, "操作失败（compose）："+err.Error(), true)
		return err
	}
	// 重建期间容器会短暂消失，让守护模块静默（与 API 通道一致）
	NoteMaintenance(serviceContext, meta.Service, 15*time.Minute)

	if skipPull {
		setMsg(40, "跳过拉取，按 compose 配置重建", false)
	} else {
		setMsg(5, "compose 容器，正在拉取新镜像", false)
		if imageNameAndTag != "" {
			if err := PullImageByRef(serviceContext, imageNameAndTag); err != nil {
				return fail(25, fmt.Errorf("拉取镜像失败: %w", err))
			}
		}
	}
	setMsg(60, fmt.Sprintf("正在 compose 重建 %s", meta.UpdateRef()), false)
	if err := composeUpService(meta, true); err != nil {
		return fail(80, err)
	}
	newID, err := findComposeContainerID(serviceContext, meta)
	if err != nil {
		// 容器没找到不一定是失败（可能 service 配置了 profiles 未启动），交由上层展示
		setMsg(100, "compose 重建完成（未能定位新容器做健康校验）", true)
		return nil
	}
	setMsg(85, "正在校验新容器运行状态", false)
	healthy, reason := WaitContainerHealthy(serviceContext, newID, func(msg string) {
		setMsg(88, msg, false)
	})
	if !healthy {
		return fail(90, fmt.Errorf("新容器健康校验失败: %s", reason))
	}
	action := "更新"
	if skipPull {
		action = "重建"
	}
	setMsg(100, action+"成功（compose）", true)
	return nil
}
