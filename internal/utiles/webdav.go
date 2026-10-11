package utiles

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"
)

// WebDAVClient 最小 WebDAV 客户端（Basic 认证），只覆盖配置备份需要的动作：
// PROPFIND（连通性/列目录）、MKCOL（建目录）、PUT（上传）、DELETE（清理旧备份）。
// 用 net/http 裸实现，不引第三方依赖；兼容群晖/坚果云/Nextcloud/Alist 等常见实现。

type WebDAVClient struct {
	base *url.URL
	user string
	pass string
	hc   *http.Client
}

// WebDAVEntry 目录项（List 返回）
type WebDAVEntry struct {
	Name  string // URL 解码后的最后一段（文件/目录名）
	IsDir bool
}

// NewWebDAVClient 校验并构造客户端。rawURL 形如 https://dav.example.com/dav/
func NewWebDAVClient(rawURL, user, pass string, timeout time.Duration) (*WebDAVClient, error) {
	raw := strings.TrimSpace(rawURL)
	if raw == "" {
		return nil, fmt.Errorf("WebDAV 地址为空")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("WebDAV 地址无效：%v", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("WebDAV 地址必须以 http:// 或 https:// 开头")
	}
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	return &WebDAVClient{base: u, user: strings.TrimSpace(user), pass: pass, hc: &http.Client{Timeout: timeout}}, nil
}

// urlFor 把"相对根地址的路径"拼成完整 URL（逐段转义；目录路径补尾斜杠）
func (c *WebDAVClient) urlFor(rel string) string {
	basePath := c.base.Path
	if !strings.HasSuffix(basePath, "/") {
		basePath += "/"
	}
	rel = strings.Trim(rel, "/")
	full := path.Join(basePath, rel)
	if rel == "" {
		full = basePath
	}
	u := *c.base
	u.Path = full
	u.RawQuery = ""
	return u.String()
}

func (c *WebDAVClient) do(ctx context.Context, method, rel string, body []byte, depth string) (*http.Response, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.urlFor(rel), rd)
	if err != nil {
		return nil, err
	}
	if c.user != "" || c.pass != "" {
		req.SetBasicAuth(c.user, c.pass)
	}
	if depth != "" {
		req.Header.Set("Depth", depth)
	}
	if method == "PROPFIND" {
		req.Header.Set("Content-Type", "application/xml; charset=utf-8")
	}
	return c.hc.Do(req)
}

// Ping 连通性检查：对"根地址 + 远端目录"做 PROPFIND Depth:0。
// 返回 (目录是否已存在, 错误)：认证失败/网络不通算错误；目录 404 不算错误（首次备份会自动创建）。
func (c *WebDAVClient) Ping(ctx context.Context, relDir string) (bool, error) {
	resp, err := c.do(ctx, "PROPFIND", relDir, nil, "0")
	if err != nil {
		return false, fmt.Errorf("连接失败：%v", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	switch resp.StatusCode {
	case 207, 200:
		return true, nil
	case 404:
		return false, nil
	case 401, 403:
		return false, fmt.Errorf("认证失败（HTTP %d）：请检查用户名/密码或应用密码", resp.StatusCode)
	default:
		return false, fmt.Errorf("服务返回 HTTP %d（期望 WebDAV 服务；请确认地址带对路径）", resp.StatusCode)
	}
}

// MkdirAll 逐段创建目录（已存在忽略）。rel 为相对根地址的目录路径。
func (c *WebDAVClient) MkdirAll(ctx context.Context, rel string) error {
	segments := strings.Split(strings.Trim(rel, "/"), "/")
	cur := ""
	for _, seg := range segments {
		if seg == "" {
			continue
		}
		cur = path.Join(cur, seg)
		resp, err := c.do(ctx, "MKCOL", cur, nil, "")
		if err != nil {
			return fmt.Errorf("创建目录 %s 失败：%v", cur, err)
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		// 201 创建成功；405 已存在；其它 2xx 也算过
		if resp.StatusCode == 201 || resp.StatusCode == 405 || (resp.StatusCode >= 200 && resp.StatusCode < 300) {
			continue
		}
		if resp.StatusCode == 401 || resp.StatusCode == 403 {
			return fmt.Errorf("创建目录 %s 被拒绝（HTTP %d）：检查账号权限", cur, resp.StatusCode)
		}
		return fmt.Errorf("创建目录 %s 失败（HTTP %d）", cur, resp.StatusCode)
	}
	return nil
}

// Put 上传文件（覆盖同路径）。rel 为相对根地址的文件路径。
func (c *WebDAVClient) Put(ctx context.Context, rel string, data []byte) error {
	resp, err := c.do(ctx, "PUT", rel, data, "")
	if err != nil {
		return fmt.Errorf("上传失败：%v", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("上传失败（HTTP %d）", resp.StatusCode)
	}
	return nil
}

// List 列目录（PROPFIND Depth:1），返回条目（不含目录自身）。
func (c *WebDAVClient) List(ctx context.Context, rel string) ([]WebDAVEntry, error) {
	resp, err := c.do(ctx, "PROPFIND", rel, nil, "1")
	if err != nil {
		return nil, fmt.Errorf("列目录失败：%v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return nil, nil // 目录还不存在 = 没有旧备份
	}
	if resp.StatusCode != 207 && resp.StatusCode != 200 {
		return nil, fmt.Errorf("列目录失败（HTTP %d）", resp.StatusCode)
	}
	var ms struct {
		Responses []struct {
			Href string `xml:"href"`
		} `xml:"response"`
	}
	if err := xml.NewDecoder(resp.Body).Decode(&ms); err != nil {
		return nil, fmt.Errorf("解析目录列表失败：%v", err)
	}
	basePath := strings.TrimSuffix(c.base.Path, "/") + "/"
	dirPath := strings.Trim(basePath+strings.Trim(rel, "/")+"/", "/")
	entries := make([]WebDAVEntry, 0, len(ms.Responses))
	for _, r := range ms.Responses {
		href := r.Href
		if u, err := url.PathUnescape(href); err == nil {
			href = u
		}
		if pu, err := url.Parse(href); err == nil {
			href = pu.Path
		}
		full := strings.Trim(href, "/")
		if full == dirPath {
			continue // 目录自身
		}
		isDir := strings.HasSuffix(href, "/")
		name := path.Base(strings.TrimSuffix(href, "/"))
		if name == "" || name == "." {
			continue
		}
		entries = append(entries, WebDAVEntry{Name: name, IsDir: isDir})
	}
	return entries, nil
}

// Delete 删除文件/目录（备份清理用）
func (c *WebDAVClient) Delete(ctx context.Context, rel string) error {
	resp, err := c.do(ctx, "DELETE", rel, nil, "")
	if err != nil {
		return fmt.Errorf("删除失败：%v", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode == 404 {
		return nil // 已不存在视为成功
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("删除失败（HTTP %d）", resp.StatusCode)
	}
	return nil
}

// PruneOldBackups 保留目录下最新的 keep 份备份（按文件名排序，时间戳命名天然按时间排）。
// prefix 为备份文件名前缀；keep<=0 不清理。返回被删除的文件名列表。
func (c *WebDAVClient) PruneOldBackups(ctx context.Context, relDir, prefix string, keep int) ([]string, error) {
	if keep <= 0 {
		return nil, nil
	}
	entries, err := c.List(ctx, relDir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir && strings.HasPrefix(e.Name, prefix) && strings.HasSuffix(strings.ToLower(e.Name), ".zip") {
			names = append(names, e.Name)
		}
	}
	sort.Strings(names) // 时间戳命名 → 字典序即时间序
	if len(names) <= keep {
		return nil, nil
	}
	var deleted []string
	for _, n := range names[:len(names)-keep] {
		if derr := c.Delete(ctx, path.Join(strings.Trim(relDir, "/"), n)); derr != nil {
			return deleted, derr
		}
		deleted = append(deleted, n)
	}
	return deleted, nil
}
