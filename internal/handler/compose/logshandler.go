package compose

import (
	"io"
	"net/http"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/libremk66/DockHamster/internal/svc"
	"github.com/zeromicro/go-zero/rest/httpx"
)

// logTailLines 日志返回的行数上限（需求约定固定 200 行）
const logTailLines = "200"

// LogsHandler 容器日志（只读，最近 200 行；TTY 容器与非 TTY 分流处理）
func LogsHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Id string `path:"id"`
		}
		if err := httpx.Parse(r, &req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		logReader, err := svcCtx.DockerClient.ContainerLogs(r.Context(), req.Id, container.LogsOptions{
			ShowStdout: true,
			ShowStderr: true,
			Tail:       logTailLines,
			Timestamps: true,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer logReader.Close()

		// 非TTY容器的日志流是 stdout/stderr 复用格式，需 demux；TTY 容器直接透传
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		insp, inspErr := svcCtx.DockerClient.ContainerInspect(r.Context(), req.Id)
		if inspErr == nil && insp.Config != nil && insp.Config.Tty {
			streamToResponse(w, logReader)
			return
		}
		_, _ = stdcopy.StdCopy(w, w, logReader)
	}
}

// streamToResponse 边读边写并即时 flush（TTY 流透传）
func streamToResponse(w http.ResponseWriter, src io.Reader) {
	buf := make([]byte, 4096)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			_, _ = w.Write(buf[:n])
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
		if err != nil {
			return
		}
	}
}
