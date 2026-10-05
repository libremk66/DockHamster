package selfupdate

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
)

// RunRelay 在接力容器内执行完整的自更新流程，返回进程退出码
func RunRelay() int {
	target := strings.TrimSpace(os.Getenv(EnvTarget))
	image := strings.TrimSpace(os.Getenv(EnvImage))
	relayLog("接力更新开始: target=%s image=%s", target, image)
	if target == "" || image == "" {
		relayLog("缺少必要环境变量，退出")
		writeResult(Result{Status: "failed", Image: image, Error: "接力容器缺少目标容器或镜像参数"})
		return 1
	}

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		relayLog("连接 Docker 失败: %v", err)
		writeResult(Result{Status: "failed", Image: image, Error: "连接 Docker 失败: " + err.Error()})
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// 给主容器留出写完任务进度、应答 HTTP 请求的时间
	time.Sleep(3 * time.Second)

	ins, err := cli.ContainerInspect(ctx, target)
	if err != nil {
		relayLog("获取目标容器信息失败: %v", err)
		writeResult(Result{Status: "failed", Image: image, Error: "获取目标容器信息失败: " + err.Error()})
		return 1
	}
	name := strings.TrimPrefix(ins.Name, "/")
	backupName := name + "-old-" + time.Now().Format("2006-01-02-15-04-05")

	stopTimeout := 15
	relayLog("停止旧容器 %s", name)
	if err := cli.ContainerStop(ctx, target, container.StopOptions{Timeout: &stopTimeout}); err != nil {
		relayLog("停止旧容器失败: %v", err)
		writeResult(Result{Status: "failed", Name: name, Image: image, Error: "停止旧容器失败: " + err.Error()})
		return 1
	}

	relayLog("重命名旧容器为 %s", backupName)
	if err := cli.ContainerRename(ctx, target, backupName); err != nil {
		relayLog("重命名旧容器失败: %v", err)
		_ = cli.ContainerStart(ctx, target, container.StartOptions{})
		writeResult(Result{Status: "failed", Name: name, Image: image, Error: "重命名旧容器失败: " + err.Error()})
		return 1
	}

	rollback := func(reason string, cleanupNew bool) {
		relayLog("更新失败开始回滚: %s", reason)
		if cleanupNew {
			removeTimeout := 5
			_ = cli.ContainerStop(ctx, name, container.StopOptions{Timeout: &removeTimeout})
			_ = cli.ContainerRemove(ctx, name, container.RemoveOptions{Force: true})
		}
		if err := cli.ContainerRename(ctx, target, name); err != nil {
			relayLog("回滚重命名失败(旧容器保留为 %s): %v", backupName, err)
		}
		if err := cli.ContainerStart(ctx, target, container.StartOptions{}); err != nil {
			relayLog("回滚启动旧容器失败: %v", err)
		} else {
			relayLog("已回滚并启动旧容器")
		}
		writeResult(Result{Status: "failed", Name: name, Image: image, Error: reason})
	}

	// 用旧容器完整配置 + 新镜像重建同名容器
	cfg := ins.Config
	cfg.Image = image
	hostCfg := ins.HostConfig
	netCfg := &network.NetworkingConfig{EndpointsConfig: ins.NetworkSettings.Networks}

	relayLog("使用新镜像创建容器 %s", name)
	if _, err := cli.ContainerCreate(ctx, cfg, hostCfg, netCfg, nil, name); err != nil {
		rollback("创建新容器失败: "+err.Error(), false)
		return 1
	}
	relayLog("启动新容器 %s", name)
	if err := cli.ContainerStart(ctx, name, container.StartOptions{}); err != nil {
		rollback("启动新容器失败: "+err.Error(), true)
		return 1
	}

	// 校验新容器稳定运行（连续 3 次检查均在运行且未处于重启循环）
	healthy := false
	for i := 0; i < 10; i++ {
		time.Sleep(2 * time.Second)
		newIns, err := cli.ContainerInspect(ctx, name)
		if err != nil {
			healthy = false
			continue
		}
		if newIns.State != nil && newIns.State.Running && !newIns.State.Restarting {
			healthy = true
			if i >= 2 {
				break
			}
			continue
		}
		healthy = false
	}
	if !healthy {
		rollback("新容器未能稳定运行", true)
		return 1
	}

	relayLog("新容器运行正常，删除备份容器 %s", backupName)
	if err := cli.ContainerRemove(ctx, target, container.RemoveOptions{}); err != nil {
		relayLog("删除备份容器失败(可手动清理 %s): %v", backupName, err)
	}
	writeResult(Result{Status: "success", Name: name, Image: image})
	relayLog("接力更新完成")
	return 0
}
