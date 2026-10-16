package auth

import (
	"net/http"
	"strconv"
	"time"

	"github.com/libremk66/DockHamster/internal/logic/auth"
	"github.com/libremk66/DockHamster/internal/svc"
	"github.com/libremk66/DockHamster/internal/types"
	"github.com/zeromicro/go-zero/rest/httpx"
)

func LoginHandler(ctx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// 失败限速：5 分钟窗口内 5 次失败 → 封 15 分钟（成功登录立即清零）
		clientKey := loginClientKey(r)
		if allowed, retryAfter := authLimiter.allow(clientKey, time.Now()); !allowed {
			w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Round(time.Second).Seconds())))
			httpx.WriteJson(w, http.StatusTooManyRequests, types.Resp{
				Code: http.StatusTooManyRequests,
				Msg:  "登录失败次数过多，请稍后重试",
				Data: map[string]interface{}{},
			})
			return
		}

		var req types.LoginReq
		if err := httpx.Parse(r, &req); err != nil {
			var resp types.Resp
			resp.Code = 400
			resp.Msg = "错误的请求"
			httpx.WriteJson(w, 400, resp)
			return
		}
		l := auth.NewLoginLogic(r.Context(), ctx)
		resp, err := l.Login(&req)
		authLimiter.record(clientKey, err == nil, time.Now())
		if err != nil {
			httpx.WriteJson(w, resp.Code, resp)
			return
		}
		httpx.OkJson(w, resp)
	}
}
