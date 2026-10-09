package utiles

// 镜像引用校验：拦住"把镜像 ID 当成可拉取的镜像名"的用法。
//
// 背景（issue #4）：容器记录里存的"镜像"如果是裸 sha256（更新对话框把列表里显示的 ID
// 原样回填 → 更新时又被原样写进新容器），会造成：
//   ① 面板显示一串用户看不懂的 ID；
//   ② 更新检测对它永远查不出结果（无 tag）；
//   ③ 每次更新都把这个 ID 再抄一遍，自我延续。
// 这里在更新入口统一拦截，并给出"应该怎么填"的提示。

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/distribution/reference"
)

// 裸镜像 ID：sha256:<hex> 或 12~64 位十六进制（短 ID）
var bareImageIDRe = regexp.MustCompile(`^(sha256:)?[0-9a-f]{12,64}$`)

// ValidateUpdateImageRef 校验并规范化"更新/拉取用的镜像名"。
//   - 拒绝裸镜像 ID（sha256:… / 短 ID）：它不是可拉取的名称，拉取必然失败
//   - 其余按 Docker 规则解析；没有标签的补 :latest（与 docker pull 行为一致）
//
// 返回规范化后的引用（如 nginx:latest）或带解释的错误。
func ValidateUpdateImageRef(ref string) (string, error) {
	r := strings.TrimSpace(ref)
	if r == "" {
		return "", errors.New("镜像名不能为空")
	}
	if bareImageIDRe.MatchString(strings.ToLower(r)) {
		return "", errors.New("这是镜像 ID 而不是可拉取的镜像名。请填「名称:标签」的形式（如 nginx:latest），" +
			"想换回正常引用可以在本对话框里改好后点「更新」")
	}
	named, err := reference.ParseDockerRef(r)
	if err != nil {
		return "", fmt.Errorf("镜像名不合法：%v（示例：nginx:latest 或 ghcr.io/owner/app:v1.2.3）", err)
	}
	// 没有 tag / digest 的（如只写 nginx）按 docker 习惯补 :latest
	return reference.TagNameOnly(named).String(), nil
}
