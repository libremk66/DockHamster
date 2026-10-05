package favicon

import (
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/libremk66/DockHamster/internal/svc"
	"github.com/libremk66/DockHamster/internal/types"
	"github.com/zeromicro/go-zero/core/logx"
	"github.com/zeromicro/go-zero/rest/httpx"
)

// ResolveHandler GET /api/favicon/resolve?url=http://host:port
// 抓取目标页面，解析 <link rel="...icon..."> 的 href，返回绝对地址；
// 失败时回落 /favicon.ico（前端 <img> 加载失败会自己降级，不额外报错）。
func ResolveHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		target := strings.TrimSpace(r.URL.Query().Get("url"))
		if !strings.Contains(target, "://") {
			target = "http://" + target
		}
		page, err := url.Parse(target)
		if err != nil || (page.Scheme != "http" && page.Scheme != "https") || page.Host == "" {
			httpx.OkJsonCtx(r.Context(), w, types.Resp{Code: 400, Msg: "invalid url", Data: map[string]string{"url": ""}})
			return
		}

		icon := resolveIcon(page)
		if icon == "" {
			icon = page.Scheme + "://" + page.Host + "/favicon.ico"
		}
		httpx.OkJsonCtx(r.Context(), w, types.Resp{Code: 200, Msg: "success", Data: map[string]string{"url": icon}})
	}
}

var (
	iconLinkRe = regexp.MustCompile(`(?is)<link[^>]*\brel\s*=\s*["']?[^"'>]*\bicon\b[^"'>]*["']?[^>]*>`)
	hrefRe     = regexp.MustCompile(`(?is)\bhref\s*=\s*["']([^"']+)["']`)
)

func resolveIcon(page *url.URL) string {
	client := &http.Client{Timeout: 4 * time.Second}
	req, err := http.NewRequest(http.MethodGet, page.String(), nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; DockHamsterFaviconResolver)")
	resp, err := client.Do(req)
	if err != nil {
		logx.Infof("favicon resolve fetch failed: %v", err)
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return ""
	}
	if ct := strings.ToLower(resp.Header.Get("Content-Type")); !strings.Contains(ct, "html") && ct != "" {
		return ""
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err != nil {
		return ""
	}

	linkTag := iconLinkRe.FindString(string(body))
	if linkTag == "" {
		return ""
	}
	m := hrefRe.FindStringSubmatch(linkTag)
	if len(m) < 2 {
		return ""
	}
	href := strings.TrimSpace(m[1])
	if strings.HasPrefix(href, "data:") {
		return href
	}
	ref, err := url.Parse(href)
	if err != nil {
		return ""
	}
	// 以重定向后的最终地址为基准做相对路径解析
	base := page
	if resp.Request != nil && resp.Request.URL != nil {
		base = resp.Request.URL
	}
	return base.ResolveReference(ref).String()
}
