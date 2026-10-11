package utiles

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
)

// 极简 WebDAV 测试服务器：PROPFIND/MKCOL/PUT/DELETE + Basic 认证
type davTestServer struct {
	mu    sync.Mutex
	files map[string][]byte // 全路径 → 内容
	dirs  map[string]bool
	user  string
	pass  string
}

func newDAVTestServer(user, pass string) *davTestServer {
	return &davTestServer{files: map[string][]byte{}, dirs: map[string]bool{"/": true}, user: user, pass: pass}
}

func (d *davTestServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if d.user != "" {
		u, p, ok := r.BasicAuth()
		if !ok || u != d.user || p != d.pass {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	p := strings.TrimSuffix(r.URL.Path, "/")
	if p == "" {
		p = "/"
	}
	switch r.Method {
	case "PROPFIND":
		if p != "/" && !d.dirs[p] && d.files[p] == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var b strings.Builder
		b.WriteString(`<?xml version="1.0"?><D:multistatus xmlns:D="DAV:">`)
		fmt.Fprintf(&b, `<D:response><D:href>%s/</D:href></D:response>`, p)
		if r.Header.Get("Depth") == "1" {
			var names []string
			prefix := p + "/"
			if p == "/" {
				prefix = "/"
			}
			for f := range d.files {
				if strings.HasPrefix(f, prefix) && !strings.Contains(strings.TrimPrefix(f, prefix), "/") {
					names = append(names, f)
				}
			}
			sort.Strings(names)
			for _, f := range names {
				fmt.Fprintf(&b, `<D:response><D:href>%s</D:href></D:response>`, f)
			}
		}
		b.WriteString(`</D:multistatus>`)
		w.WriteHeader(207)
		_, _ = w.Write([]byte(b.String()))
	case "MKCOL":
		if d.dirs[p] || d.files[p] != nil {
			w.WriteHeader(405) // 已存在
			return
		}
		d.dirs[p] = true
		w.WriteHeader(201)
	case "PUT":
		body, _ := io.ReadAll(r.Body)
		d.files[p] = body
		w.WriteHeader(201)
	case "DELETE":
		if d.files[p] == nil {
			w.WriteHeader(404)
			return
		}
		delete(d.files, p)
		w.WriteHeader(204)
	default:
		w.WriteHeader(405)
	}
}

func TestWebDAVClientFlow(t *testing.T) {
	srv := newDAVTestServer("user", "pass")
	ts := httptest.NewServer(srv)
	defer ts.Close()
	ctx := context.Background()

	c, err := NewWebDAVClient(ts.URL+"/dav/", "user", "pass", 0)
	if err != nil {
		t.Fatal(err)
	}

	// ① 目录不存在：Ping 不报错，exists=false（根 /dav 也不存在，先确认 404 分支）
	if _, err := c.Ping(ctx, "backup/compose"); err != nil {
		t.Fatalf("Ping 不应因目录不存在而报错: %v", err)
	}

	// ② 建目录 + 上传
	if err := c.MkdirAll(ctx, "backup/compose"); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if _, err := c.Ping(ctx, "backup/compose"); err != nil {
		t.Fatalf("建目录后 Ping: %v", err)
	}
	if err := c.Put(ctx, "backup/compose/a.zip", []byte("hello")); err != nil {
		t.Fatalf("Put: %v", err)
	}

	// ③ 列目录
	entries, err := c.List(ctx, "backup/compose")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "a.zip" || entries[0].IsDir {
		t.Fatalf("List 结果不对: %+v", entries)
	}

	// ④ 保留策略：7 个备份保留 3 份，删掉最旧的 4 个
	for i := 1; i <= 7; i++ {
		name := fmt.Sprintf("dockhamster-compose-2026100%d-030000.zip", i)
		if err := c.Put(ctx, "backup/compose/"+name, []byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.Put(ctx, "backup/compose/其它文件.txt", []byte("keep")); err != nil {
		t.Fatal(err)
	}
	deleted, err := c.PruneOldBackups(ctx, "backup/compose", "dockhamster-compose-", 3)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if len(deleted) != 4 {
		t.Fatalf("应删 4 份，实际 %d：%v", len(deleted), deleted)
	}
	entries, _ = c.List(ctx, "backup/compose")
	var keptCompose, keptOther int
	for _, e := range entries {
		if strings.HasPrefix(e.Name, "dockhamster-compose-") {
			keptCompose++
		} else {
			keptOther++
		}
	}
	// 非 compose 命名的文件不参与清理：a.zip（步骤②上传）+ 其它文件.txt 共 2 个
	if keptCompose != 3 || keptOther != 2 {
		t.Fatalf("保留结果不对：compose=%d other=%d", keptCompose, keptOther)
	}

	// ⑤ 认证失败分支
	c2, _ := NewWebDAVClient(ts.URL+"/dav/", "user", "wrong", 0)
	if _, err := c2.Ping(ctx, "backup"); err == nil || !strings.Contains(err.Error(), "认证") {
		t.Fatalf("应报认证失败: %v", err)
	}

	// ⑥ 地址校验
	if _, err := NewWebDAVClient("ftp://x", "", "", 0); err == nil {
		t.Fatal("非 http(s) 地址应被拒绝")
	}
}
