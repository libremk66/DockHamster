package utiles

import (
	"io"
	"strings"
	"testing"

	dockerMsgType "github.com/docker/docker/pkg/jsonmessage"
)

func TestPullMessageSucceeded(t *testing.T) {
	if !pullMessageSucceeded(dockerMsgType.JSONMessage{Status: "Status: Downloaded newer image for nginx:latest"}) {
		t.Fatal("Downloaded newer image 应视为成功")
	}
	if !pullMessageSucceeded(dockerMsgType.JSONMessage{Status: "Image is up to date for nginx:latest"}) {
		t.Fatal("Image is up to date 应视为成功")
	}
	if pullMessageSucceeded(dockerMsgType.JSONMessage{Status: "Pulling from library/nginx"}) {
		t.Fatal("中间层状态不应视为成功")
	}
}

func TestConsumePullStreamRequiresSuccess(t *testing.T) {
	ok := `{"status":"Pulling from library/nginx"}` + "\n" +
		`{"status":"Status: Downloaded newer image for nginx:latest"}` + "\n"
	if err := consumePullStream(io.NopCloser(strings.NewReader(ok))); err != nil {
		t.Fatalf("完整流应成功: %v", err)
	}

	truncated := `{"status":"Pulling from library/nginx"}` + "\n"
	if err := consumePullStream(io.NopCloser(strings.NewReader(truncated))); err == nil {
		t.Fatal("没有成功确认的 EOF 应报错")
	}
}
