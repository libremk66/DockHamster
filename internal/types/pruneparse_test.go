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
