package main

import (
	"embed"
	"flag"
	"fmt"
	"github.com/libremk66/DockHamster/internal/notify"
	"go/types"
	"io/fs"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/libremk66/DockHamster/internal/config"
	"github.com/libremk66/DockHamster/internal/handler"
	"github.com/libremk66/DockHamster/internal/logic/autoupdate"
	"github.com/libremk66/DockHamster/internal/selfupdate"
	"github.com/libremk66/DockHamster/internal/svc"
	"github.com/libremk66/DockHamster/internal/utiles"
	"github.com/libremk66/DockHamster/internal/watchdog"
	"github.com/robfig/cron/v3"
	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/core/logx"
	"github.com/zeromicro/go-zero/rest"
	"github.com/zeromicro/go-zero/rest/httpx"
	"github.com/zeromicro/x/errors"
	xhttp "github.com/zeromicro/x/http"
)

//go:embed front/*
var embeddedFront embed.FS

var configFile = flag.String("f", "etc/dockhamster.yaml", "the config file")

type UnauthorizedResponse struct {
	Code int                    `json:"code"`
	Msg  string                 `json:"msg"`
	Data map[string]interface{} `json:"data"`
}

func main() {
	// 接力自更新模式：由 selfupdate.Launch 以新镜像启动的一次性容器进入（必须最先判断）
	if os.Getenv(selfupdate.EnvFlag) == "1" {
		os.Exit(selfupdate.RunRelay())
	}

	logDir := "./logs"
	ErrSetupLog := SetupLog(logDir)
	if ErrSetupLog != nil {
		logx.Errorf("failed to setup log: %v", ErrSetupLog)
		os.Exit(1)
	}
	logx.SetLevel(logx.InfoLevel)

	flag.Parse()
	var c config.Config
	err := conf.Load(*configFile, &c, conf.UseEnv())
	if err != nil {
		logx.Errorf("无法加载配置文件出错: %v", err)
		logx.Errorf("请确认secretKey设置正确，要求非纯数字且大于八位")
		os.Exit(1)
	}
	server := rest.MustNewServer(c.RestConf, rest.WithCors("*"), rest.WithUnauthorizedCallback(
		func(w http.ResponseWriter, r *http.Request, err error) {
			response := UnauthorizedResponse{
				Code: http.StatusUnauthorized, // 401
				Msg:  "未授权",
				Data: map[string]interface{}{},
			}
			httpx.WriteJson(w, http.StatusUnauthorized, response)
		}))
	defer server.Stop()
	ctx := svc.NewServiceContext(c)

	// 上次自更新结果上报（如有）：写日志 + 发通知，结果只消费一次
	go selfupdate.ReportResultOnBoot(ctx)

	// 容器守护：巡检异常退出 / OOM / 重启循环 → 通知（可在自动更新页关闭）
	ctx.Watchdog = watchdog.New(ctx.DockerClient, watchdog.Config{
		Disabled: func() bool { return ctx.AutoUpdate.Get().WatchdogDisabled },
		Notify: func(title, text string) {
			notify.Send(ctx.AutoUpdate.Get().Notify, title, text)
		},
	})
	ctx.Watchdog.Start()

	// Ensure data directory and config exist (Auto-init)
	dataDir := "/data/config/image"
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		logx.Errorf("Failed to create data directory: %v", err)
	}

	imageLogosPath := "/data/config/imageLogos.js"
	if _, err := os.Stat(imageLogosPath); os.IsNotExist(err) {
		defaultConfig := []byte(`// 自定义镜像logo配置
export const customImageLogos = {
};
`)
		if err := os.WriteFile(imageLogosPath, defaultConfig, 0644); err != nil {
			logx.Errorf("Failed to create default imageLogos.js: %v", err)
		}
	}

	// 启动时先查一遍（之后由"检查更新 cron"按设置频率接管）
	go func() {
		if _, _, cerr := utiles.CheckAllImageUpdates(ctx, "startup"); cerr != nil {
			logx.Errorf("启动检查更新失败: %v", cerr)
		}
	}()

	// 进度记录清理：已完成/失败的任务保留 2 小时（「任务」页可回看），之后从内存移除
	go func() {
		for {
			time.Sleep(10 * time.Minute)
			if n := ctx.PurgeFinishedProgress(2 * time.Hour); n > 0 {
				logx.Infof("已清理 %d 条历史任务进度", n)
			}
		}
	}()
	corndanmu := cron.New(cron.WithParser(cron.NewParser(
		cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow,
	)))
	// 自动更新调度：注册到 cron 引擎（设置由 UI 持久化，改 cron 会重注册）
	ctx.CronEngine = corndanmu
	corndanmu.Start()
	if err := autoupdate.RegisterCrons(ctx); err != nil {
		logx.Errorf("自动更新定时任务注册失败(检查 cron 表达式): %v", err)
	} else {
		logx.Info("自动更新任务已注册，调度: " + ctx.AutoUpdate.Get().Cron)
	}
	defer corndanmu.Stop()
	httpx.SetErrorHandler(func(err error) (int, any) {
		switch e := err.(type) {
		case *errors.CodeMsg:
			return http.StatusOK, xhttp.BaseResponse[types.Nil]{
				Code: e.Code,
				Msg:  e.Msg,
			}
		default:
			return http.StatusOK, xhttp.BaseResponse[types.Nil]{
				Code: 50000,
				Msg:  err.Error(),
			}
		}
	})
	handler.RegisterHandlers(server, ctx)
	RegisterHandlers(server)
	fmt.Printf("Starting server at %s:%d...\n", c.Host, c.Port)
	logx.Info("程序版本" + config.Version)
	server.Start()
}
func RegisterHandlers(engine *rest.Server) {
	frontFS, err := fs.Sub(embeddedFront, "front")
	if err != nil {
		log.Fatal(err)
	}

	frontFileServer := http.StripPrefix("/manager", http.FileServer(http.FS(frontFS)))

	assetsHandler := http.FileServer(http.FS(frontFS))

	// Serve custom icons
	iconFileServer := http.StripPrefix("/src/config/image/", http.FileServer(http.Dir("/data/config/image")))
	engine.AddRoutes(
		[]rest.Route{
			{
				Method: http.MethodGet,
				Path:   "/src/config/image/:file",
				Handler: func(w http.ResponseWriter, r *http.Request) {
					iconFileServer.ServeHTTP(w, r)
				},
			},
		},
	)

	engine.AddRoutes(
		[]rest.Route{
			{
				Method: http.MethodGet,
				Path:   "/manager",
				Handler: func(w http.ResponseWriter, r *http.Request) {
					frontFileServer.ServeHTTP(w, r)
				},
			},
			{
				Method: http.MethodGet,
				Path:   "/manager/:path",
				Handler: func(w http.ResponseWriter, r *http.Request) {
					frontFileServer.ServeHTTP(w, r)
				},
			},
			{
				Method: http.MethodGet,
				Path:   "/manager/assets/:path",
				Handler: func(w http.ResponseWriter, r *http.Request) {
					frontFileServer.ServeHTTP(w, r)
				},
			},
			{
				Method: http.MethodGet,
				Path:   "/assets/:path",
				Handler: func(w http.ResponseWriter, r *http.Request) {
					assetsHandler.ServeHTTP(w, r)
				},
			},
		},
	)
}

// 检查并创建日志目录
func ensureLogDirectory(logDir string) error {
	if _, err := os.Stat(logDir); os.IsNotExist(err) {
		return os.MkdirAll(logDir, 0755) // 创建目录并设置权限
	}
	return nil
}

// SetupLog 初始化日志设置
func SetupLog(logDir string) error {
	// 检查日志目录是否存在
	if err := ensureLogDirectory(logDir); err != nil {
		return fmt.Errorf("failed to create log directory: %v", err)
	}

	logConf := logx.LogConf{
		Path:     logDir,
		Level:    "info",
		KeepDays: 7,
		Compress: true,
		Mode:     "file",
	}
	logx.MustSetup(logConf)
	logx.AddWriter(logx.NewWriter(os.Stdout))
	return nil
}
