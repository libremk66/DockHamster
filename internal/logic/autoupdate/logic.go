package autoupdate

import (
	"context"
	"strings"
	"time"

	"github.com/onlyLTY/dockerCopilot/internal/module"
	"github.com/onlyLTY/dockerCopilot/internal/svc"
	"github.com/onlyLTY/dockerCopilot/internal/types"
	"github.com/onlyLTY/dockerCopilot/internal/utiles"
	"github.com/zeromicro/go-zero/core/logx"
)

type AutoUpdateLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewAutoUpdateLogic(ctx context.Context, svcCtx *svc.ServiceContext) *AutoUpdateLogic {
	return &AutoUpdateLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// GetSettings 读取自动更新设置
func (l *AutoUpdateLogic) GetSettings() (*types.Resp, error) {
	resp := &types.Resp{Code: 200, Msg: "success"}
	resp.Data = l.svcCtx.AutoUpdate.Get()
	return resp, nil
}

// SaveSettings 保存设置并重注册定时任务
func (l *AutoUpdateLogic) SaveSettings(req *module.AutoUpdateSettings) (*types.Resp, error) {
	resp := &types.Resp{}
	if err := ValidateCron(req.Cron); err != nil {
		resp.Code = 400
		resp.Msg = "cron 表达式无效：" + err.Error()
		resp.Data = map[string]interface{}{}
		return resp, nil
	}
	if err := l.svcCtx.AutoUpdate.Save(*req); err != nil {
		resp.Code = 500
		resp.Msg = "保存失败：" + err.Error()
		resp.Data = map[string]interface{}{}
		return resp, nil
	}
	if err := RegisterAutoUpdateCron(l.svcCtx); err != nil {
		resp.Code = 500
		resp.Msg = "定时任务注册失败：" + err.Error()
		resp.Data = map[string]interface{}{}
		return resp, nil
	}
	resp.Code = 200
	resp.Msg = "success"
	resp.Data = l.svcCtx.AutoUpdate.Get()
	return resp, nil
}

// Run 立即运行一轮自动更新（异步）
func (l *AutoUpdateLogic) Run() (*types.Resp, error) {
	resp := &types.Resp{}
	settings := l.svcCtx.AutoUpdate.Get()
	if len(module.SplitNameList(strings.Join(settings.Containers, ","))) == 0 {
		resp.Code = 400
		resp.Msg = "白名单为空，请先在页面上勾选要自动更新的容器"
		resp.Data = map[string]interface{}{}
		return resp, nil
	}
	if l.svcCtx.AutoUpdateState.IsRunning() {
		resp.Code = 409
		resp.Msg = "已有更新任务正在运行，请稍后再试"
		resp.Data = map[string]interface{}{}
		return resp, nil
	}
	go utiles.RunAutoUpdate(l.svcCtx, "manual")
	resp.Code = 200
	resp.Msg = "已开始运行"
	resp.Data = map[string]interface{}{}
	return resp, nil
}

// Status 运行状态与最近记录
func (l *AutoUpdateLogic) Status() (*types.Resp, error) {
	running, runs, last := l.svcCtx.AutoUpdateState.Snapshot()
	resp := &types.Resp{Code: 200, Msg: "success"}
	resp.Data = map[string]interface{}{
		"running":    running,
		"runs":       runs,
		"lastStatus": last,
	}
	return resp, nil
}

// TestNotify 发送一条飞书测试消息（可临时覆盖 webhook）
func (l *AutoUpdateLogic) TestNotify(req *types.TestNotifyReq) (*types.Resp, error) {
	resp := &types.Resp{}
	webhook := strings.TrimSpace(req.Webhook)
	if webhook == "" {
		webhook = l.svcCtx.AutoUpdate.Get().FeishuWebhook
	}
	if webhook == "" {
		resp.Code = 400
		resp.Msg = "未配置飞书 Webhook（可先在输入框填入再测试）"
		resp.Data = map[string]interface{}{}
		return resp, nil
	}
	msg := "🔔 这是一条来自 DockerCopilot 的测试消息，通知配置正常。\n时间：" + time.Now().Format("2006-01-02 15:04:05")
	if err := module.SendFeishu(webhook, msg); err != nil {
		resp.Code = 500
		resp.Msg = "发送失败：" + err.Error()
		resp.Data = map[string]interface{}{}
		return resp, nil
	}
	resp.Code = 200
	resp.Msg = "已发送，请查看飞书群"
	resp.Data = map[string]interface{}{}
	return resp, nil
}

// UpdateGroup 整组更新：更新与该容器共用同一镜像的所有容器（方案 A「全部更新」）
func (l *AutoUpdateLogic) UpdateGroup(req *types.GroupUpdateReq) (*types.Resp, error) {
	resp := &types.Resp{}
	tasks, err := utiles.RunGroupUpdate(l.svcCtx, req.Id)
	if err != nil {
		resp.Code = 500
		resp.Msg = err.Error()
		resp.Data = map[string]interface{}{}
		return resp, nil
	}
	resp.Code = 200
	resp.Msg = "success"
	resp.Data = map[string]interface{}{"tasks": tasks}
	return resp, nil
}
