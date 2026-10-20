package utiles

import (
	"context"
	"errors"
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

// ErrComposeUnavailable 标记"面板侧 compose 环境不具备"：容器内没有 compose 命令，
// 或 compose 文件在面板容器里不可达（未挂载）。调用方据此**回退 API 重建通道**并显式提示；
// 注意只涵盖"环境不可用"——compose up 真正执行失败仍按错误处理，不回退（避免配置漂移）。
var ErrComposeUnavailable = errors.New("compose 通道不可用")

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
	return "", nil, fmt.Errorf("%w：面板容器内未找到 docker compose / docker-compose", ErrComposeUnavailable)
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

// ResolveConfigFiles 将 compose 文件路径解析为面板容器内可达的路径（仅环境变量备用根；
// 更新通道请用 ResolveConfigFilesEx + ComposeAltRoots，把「Compose」页设置的额外根目录也带上）。
func ResolveConfigFiles(paths []string) ([]string, error) {
	return resolveConfigFilesWith(paths, altRootPrefixes())
}

// ResolveConfigFilesEx 同 ResolveConfigFiles，但显式传入备用根
// （调用方通常传 ComposeAltRoots —— 环境变量 + 设置的合并结果）
func ResolveConfigFilesEx(paths []string, roots []string) ([]string, error) {
	return resolveConfigFilesWith(paths, roots)
}

// resolveConfigFilesWith 将 compose 文件路径解析为面板容器内可达的路径。
// labels 记录的是容器创建时宿主机视角的路径，面板容器内该路径不一定一致。
// 策略：原路径可达则原样使用；否则按备用根前缀逐一探测替换，全部不可达才报错。
func resolveConfigFilesWith(paths []string, roots []string) ([]string, error) {
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
			return nil, fmt.Errorf("%w：compose 文件不可达 %s（需在面板容器挂载该路径，或在「Compose」页设置额外根目录）", ErrComposeUnavailable, p)
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
func composeUpService(serviceContext *svc.ServiceContext, meta ComposeMeta, force bool) error {
	bin, subcmd, err := composeBinary()
	if err != nil {
		return err
	}
	roots := ComposeAltRoots(serviceContext)
	args := append([]string{}, subcmd...)
	files, err := ResolveConfigFilesSmart(serviceContext, meta.ConfigFiles, roots)
	if err != nil {
		return err
	}
	for _, f := range files {
		args = append(args, "-f", f)
	}
	// 项目目录同样走统一解析（Portainer 栈的 working_dir 也是容器内视角 /data/compose/<id>）；
	// 解析不到就退回第一个 compose 文件所在目录（compose 的默认语义）
	projectDir := ""
	if meta.WorkingDir != "" {
		if r, dErr := ResolveConfigFilesSmart(serviceContext, []string{meta.WorkingDir}, roots); dErr == nil && len(r) > 0 {
			projectDir = r[0]
		}
	}
	if projectDir == "" && len(files) > 0 {
		projectDir = filepath.Dir(files[0])
	}
	if projectDir != "" {
		args = append(args, "--project-directory", projectDir)
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
	setMsg := func(pct int, msg string, done, failed bool) {
		serviceContext.UpdateProgress(taskID, svc.TaskProgress{
			TaskID:     taskID,
			Percentage: pct,
			Name:       meta.UpdateRef(),
			Message:    msg,
			DetailMsg:  msg,
			IsDone:     done,
			Failed:     failed,
		})
	}
	fail := func(pct int, err error) error {
		if errors.Is(err, ErrComposeUnavailable) {
			// 环境不可用会由调用方回退 API 重建，不算本次更新失败：
			// 不能落终态，否则历史里会多一条误导性的失败记录（回退成功也留着红星）
			setMsg(pct, "compose 通道不可用，回退 API 重建："+err.Error(), false, false)
			return err
		}
		// 失败必须带 Failed 标记：只置 IsDone 会被前端当成"完成"（历史里也显示成功）
		setMsg(pct, "操作失败（compose）："+err.Error(), true, true)
		return err
	}
	// 重建期间容器会短暂消失，让守护模块静默（与 API 通道一致）
	NoteMaintenance(serviceContext, meta.Service, 15*time.Minute)

	if skipPull {
		setMsg(40, "跳过拉取，按 compose 配置重建", false, false)
	} else {
		setMsg(5, "compose 容器，正在拉取新镜像", false, false)
		if imageNameAndTag != "" {
			if err := PullImageByRefForTasks(serviceContext, imageNameAndTag, []string{taskID}); err != nil {
				return fail(25, fmt.Errorf("拉取镜像失败: %w", err))
			}
		}
	}
	setMsg(60, fmt.Sprintf("正在 compose 重建 %s", meta.UpdateRef()), false, false)
	if err := composeUpService(serviceContext, meta, true); err != nil {
		return fail(80, err)
	}
	newID, err := findComposeContainerID(serviceContext, meta)
	if err != nil {
		// 容器没找到不一定是失败（可能 service 配置了 profiles 未启动），交由上层展示
		setMsg(100, "compose 重建完成（未能定位新容器做健康校验）", true, false)
		return nil
	}
	setMsg(85, "正在校验新容器运行状态", false, false)
	healthy, reason := WaitContainerHealthy(serviceContext, newID, func(msg string) {
		setMsg(88, msg, false, false)
	})
	if !healthy {
		return fail(90, fmt.Errorf("新容器健康校验失败: %s", reason))
	}
	action := "更新"
	if skipPull {
		action = "重建"
	}
	setMsg(100, action+"成功（compose）", true, false)
	return nil
}
