package selfupdate

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	dockerTypes "github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/libremk66/DockHamster/internal/module"
	"github.com/libremk66/DockHamster/internal/svc"
	"github.com/libremk66/DockHamster/internal/utiles"
	"github.com/zeromicro/go-zero/core/logx"
)

// Launch 在主容器内发起面板自更新：拉取新镜像并启动接力容器。
// 返回后主容器等着被接力容器停掉替换，前端轮询会看到"接力容器已启动"。
func Launch(svcCtx *svc.ServiceContext, selfID, name, imageRef, taskID string) error {
	ctx := context.Background()
	progress := func(pct int, msg string, done bool) {
		svcCtx.UpdateProgress(taskID, svc.TaskProgress{
			TaskID:     taskID,
			Percentage: pct,
			Name:       name,
			Message:    msg,
			DetailMsg:  msg,
			IsDone:     done,
		})
	}

	progress(5, "正在拉取新镜像 "+imageRef, false)
	if err := utiles.PullImageForUpdate(svcCtx, taskID, imageRef); err != nil {
		progress(100, "拉取镜像失败: "+err.Error(), true)
		return err
	}

	selfIns, err := svcCtx.DockerClient.ContainerInspect(ctx, selfID)
	if err != nil {
		progress(100, "获取自身容器信息失败: "+err.Error(), true)
		return err
	}

	// 拉到的和当前跑的是同一个镜像就不用折腾了
	if imgIns, _, ierr := svcCtx.DockerClient.ImageInspectWithRaw(ctx, imageRef); ierr == nil {
		if strings.TrimSpace(imgIns.ID) != "" && imgIns.ID == selfIns.Image {
			progress(100, "已是最新镜像，无需更新", true)
			logx.Infof("自更新：镜像 %s 与当前一致，跳过重建", imageRef)
			return nil
		}
	}

	progress(50, "正在准备接力更新容器", false)

	// 清理可能残留的旧接力容器
	_ = svcCtx.DockerClient.ContainerRemove(ctx, UpdaterName, container.RemoveOptions{Force: true})

	binds, mounts := relayVolumes(selfIns)
	env := []string{
		EnvFlag + "=1",
		EnvTarget + "=" + selfIns.ID,
		EnvImage + "=" + imageRef,
	}
	for _, key := range []string{"DOCKER_HOST", "TZ", "DOCKER_API_VERSION", "DOCKER_CERT_PATH", "DOCKER_TLS_VERIFY"} {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			env = append(env, key+"="+v)
		}
	}

	cfg := &container.Config{
		Image:      imageRef, // 用新镜像跑接力逻辑（新镜像里含本包）
		Entrypoint: []string{"/app/dockhamster"},
		Cmd:        []string{},
		Env:        env,
		WorkingDir: "/app",
		Labels:     map[string]string{"dockhamster.role": "self-updater"},
	}
	hostCfg := &container.HostConfig{
		Binds:      binds,
		Mounts:     mounts,
		AutoRemove: true,
		Privileged: selfIns.HostConfig != nil && selfIns.HostConfig.Privileged,
	}
	if _, err := svcCtx.DockerClient.ContainerCreate(ctx, cfg, hostCfg, nil, nil, UpdaterName); err != nil {
		progress(100, "创建接力更新容器失败: "+err.Error(), true)
		return err
	}
	if err := svcCtx.DockerClient.ContainerStart(ctx, UpdaterName, container.StartOptions{}); err != nil {
		_ = svcCtx.DockerClient.ContainerRemove(ctx, UpdaterName, container.RemoveOptions{Force: true})
		progress(100, "启动接力更新容器失败: "+err.Error(), true)
		return err
	}

	progress(100, "接力容器已启动，面板即将重启，约 20 秒后自动恢复", true)
	logx.Infof("自更新接力容器已启动，等待被替换: image=%s", imageRef)
	return nil
}

// relayVolumes 从自身容器的挂载里筛出接力容器需要的：docker socket（操作 Docker）+ /data（写结果）
func relayVolumes(ins dockerTypes.ContainerJSON) ([]string, []mount.Mount) {
	binds := make([]string, 0, 2)
	mounts := make([]mount.Mount, 0, 2)
	if ins.HostConfig == nil {
		return binds, mounts
	}
	for _, b := range ins.HostConfig.Binds {
		if strings.Contains(b, "docker.sock") || bindTarget(b) == "/data" {
			binds = append(binds, b)
		}
	}
	for _, m := range ins.HostConfig.Mounts {
		if strings.Contains(m.Source, "docker.sock") || m.Target == "/var/run/docker.sock" || m.Target == "/data" {
			mounts = append(mounts, m)
		}
	}
	return binds, mounts
}

// bindTarget 提取 bind 声明(src:dst[:opts])里的容器内路径
func bindTarget(b string) string {
	parts := strings.Split(b, ":")
	if len(parts) >= 2 {
		return parts[1]
	}
	return ""
}

// ReportResultOnBoot 新容器启动后上报上次自更新结果（写日志 + 发通知），结果只上报一次。
// 接力容器写完结果可能比新面板启动晚几秒，所以启动后短暂轮询等待。
func ReportResultOnBoot(svcCtx *svc.ServiceContext) {
	var (
		res Result
		ok  bool
	)
	for i := 0; i < 30; i++ {
		if res, ok = ConsumeResult(); ok {
			break
		}
		time.Sleep(2 * time.Second)
	}
	if !ok {
		return
	}
	title := "🔄 面板自更新成功"
	text := fmt.Sprintf("已完成自更新\n镜像：%s\n时间：%s", res.Image, res.At)
	if res.Status != "success" {
		title = "⚠️ 面板自更新失败（已回滚）"
		text = fmt.Sprintf("更新到 %s 失败：%s\n已自动回滚旧版本\n时间：%s", res.Image, res.Error, res.At)
	}
	logx.Info(title + " | " + strings.ReplaceAll(text, "\n", " "))
	setLastResult(res)
	module.SendNotify(svcCtx.AutoUpdate.Get().Notify, title, text)
}
