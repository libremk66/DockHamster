package handler

import (
	"net/http"
)

// SecurityHeaders 全局安全响应头（浏览器侧防护，一行成本）。
//
//   - X-Frame-Options: DENY —— 防点击劫持（恶意页面用 iframe 套住面板，
//     诱导已登录用户点"删除/清理"）
//   - X-Content-Type-Options: nosniff —— 防 MIME 嗅探（浏览器把非脚本当脚本执行）
//   - Referrer-Policy: no-referrer —— 不外泄面板地址给第三方
//
// 注意：不发 HSTS（面板默认 http 直连，发了会给用户添乱）；不发 CSP
// （SPA 内联样式多，容易误伤，收益与风险不成比例）。
func SecurityHeaders(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		next(w, r)
	}
}
