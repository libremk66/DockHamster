package types

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zeromicro/go-zero/rest/httpx"
)

func TestPruneResourcesReqJSONBody(t *testing.T) {
	body := `{"volumes":true,"projectVolumes":false,"networks":false}`
	req := httptest.NewRequest(http.MethodPost, "/api/resources/prune", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	var v PruneResourcesReq
	if err := httpx.Parse(req, &v); err != nil {
		t.Fatalf("parse error: %v", err)
	}
	t.Logf("parsed: %+v", v)
	if !v.Volumes || v.ProjectVolumes || v.Networks {
		t.Fatalf("JSON body 被忽略: %+v", v)
	}
}

// form 调用方式仍需兼容（保留 form tag 的回归保护）。
func TestPruneResourcesReqFormBody(t *testing.T) {
	body := "volumes=true&projectVolumes=false&networks=true"
	req := httptest.NewRequest(http.MethodPost, "/api/resources/prune", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var v PruneResourcesReq
	if err := httpx.Parse(req, &v); err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if !v.Volumes || v.ProjectVolumes || !v.Networks {
		t.Fatalf("form body 解析错误: %+v", v)
	}
}

// 逐卷勾选的名单：JSON body 必须能解析（go-zero 会跳过无 json tag 的字段，v1.7.2 的 #9 教训）
func TestPruneResourcesReqProjectNamesJSON(t *testing.T) {
	body := `{"projectVolumeNames":["demoapp_cache-data","legacy_dbdata"]}`
	req := httptest.NewRequest(http.MethodPost, "/api/resources/prune", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	var v PruneResourcesReq
	if err := httpx.Parse(req, &v); err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(v.ProjectVolumeNames) != 2 || v.ProjectVolumeNames[0] != "demoapp_cache-data" {
		t.Fatalf("projectVolumeNames 未解析: %+v", v)
	}
}

// form 方式传名单：go-zero 会把整个值当成一个元素（"a,b"），
// 由 utiles.filterProjectVolumesByName 再按逗号拆分（UI 实际走 JSON body，这里只保证不丢值）。
func TestPruneResourcesReqProjectNamesForm(t *testing.T) {
	body := `projectVolumeNames=a,b`
	req := httptest.NewRequest(http.MethodPost, "/api/resources/prune", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var v PruneResourcesReq
	if err := httpx.Parse(req, &v); err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(v.ProjectVolumeNames) != 1 || v.ProjectVolumeNames[0] != "a,b" {
		t.Fatalf("form 名单未解析: %+v", v)
	}
}
