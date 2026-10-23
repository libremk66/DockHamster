package module

import (
	"context"
	"errors"
	"testing"

	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/registry"
	"github.com/libremk66/DockHamster/internal/types"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// fakeInspector 模拟守护进程的 DistributionInspect（记录收到的凭据）
type fakeInspector struct {
	digest  map[string]string
	errs    map[string]error
	auths   map[string]string
	lastRef string
}

func (f *fakeInspector) DistributionInspect(_ context.Context, image, encodedRegistryAuth string) (registry.DistributionInspect, error) {
	f.lastRef = image
	if f.auths == nil {
		f.auths = map[string]string{}
	}
	f.auths[image] = encodedRegistryAuth
	if err, ok := f.errs[image]; ok {
		return registry.DistributionInspect{}, err
	}
	d, ok := f.digest[image]
	if !ok {
		return registry.DistributionInspect{}, errors.New("manifest unknown")
	}
	return registry.DistributionInspect{Descriptor: ocispec.Descriptor{Digest: digest.Digest(d)}}, nil
}

func TestCheckSingleImageUnauthorizedIsAuthReason(t *testing.T) {
	f := &fakeInspector{errs: map[string]error{
		"ghcr.io/o/private:latest": errors.New(`Head "https://ghcr.io/v2/o/private/manifests/latest": unauthorized`),
	}}
	i := NewImageCheck()
	deps := CheckUpdateDeps{}
	res := i.checkSingleImage(f, types.Image{
		Summary: image.Summary{
			RepoTags:    []string{"ghcr.io/o/private:latest"},
			RepoDigests: []string{"ghcr.io/o/private@sha256:old"},
		},
		ImageName: "ghcr.io/o/private", ImageTag: "latest",
	}, deps)
	if res == nil || !res.Uncheckable || res.Reason != CheckReasonAuth {
		t.Fatalf("unauthorized 应标记 auth，得到 %+v", res)
	}
}

func TestCheckSingleImageNotFoundReason(t *testing.T) {
	f := &fakeInspector{errs: map[string]error{
		"example.com/o/gone:latest": errors.New("manifest unknown"),
	}}
	i := NewImageCheck()
	img := types.Image{
		Summary: image.Summary{
			RepoTags:    []string{"example.com/o/gone:latest"},
			RepoDigests: []string{"example.com/o/gone@sha256:old"},
		},
		ImageName: "example.com/o/gone", ImageTag: "latest",
	}
	res := i.checkSingleImage(f, img, CheckUpdateDeps{})
	if res == nil || !res.Uncheckable || res.Reason != CheckReasonNotFound {
		t.Fatalf("manifest unknown 应标记 not-found，得到 %+v", res)
	}
}

func TestCheckSingleImageTransientKeepsState(t *testing.T) {
	f := &fakeInspector{errs: map[string]error{
		"localhost:1/foo/bar:latest": errors.New("dial tcp: i/o timeout"),
	}}
	i := NewImageCheck()
	img := types.Image{
		Summary: image.Summary{
			RepoTags:    []string{"localhost:1/foo/bar:latest"},
			RepoDigests: []string{"localhost:1/foo/bar@sha256:old"},
		},
		ImageName: "localhost:1/foo/bar",
		ImageTag:  "latest",
	}
	if res := i.checkSingleImage(f, img, CheckUpdateDeps{}); res != nil {
		t.Fatalf("暂态错误应返回 nil（保留旧状态），得到 %+v", res)
	}
}

func TestCheckSingleImageNoTagWithoutRef(t *testing.T) {
	i := NewImageCheck()
	res := i.checkSingleImage(&fakeInspector{}, types.Image{
		ImageName: "None", ImageTag: "None",
	}, CheckUpdateDeps{})
	if res == nil || !res.Uncheckable || res.Reason != CheckReasonNoTag {
		t.Fatalf("无 tag 且无引用应标记 no-tag，得到 %+v", res)
	}
}

func TestCheckSingleImageNoTagUsesRefFor(t *testing.T) {
	f := &fakeInspector{digest: map[string]string{
		"docker.io/acme/media-api:latest": "sha256:newdigest",
	}}
	i := NewImageCheck()
	img := types.Image{
		Summary:   image.Summary{RepoDigests: []string{"acme/media-api@sha256:olddigest"}},
		ImageName: "acme/media-api",
		ImageTag:  "None", // 列表里没有 tag
	}
	res := i.checkSingleImage(f, img, CheckUpdateDeps{RefFor: func(string) string { return "acme/media-api:latest" }})
	if res == nil {
		t.Fatal("有 RefFor 时应正常检测")
	}
	if res.Uncheckable {
		t.Fatalf("有可用引用时不应标记无法检测: %+v", res)
	}
	if !res.NeedUpdate {
		t.Fatalf("远端 digest 不同应有更新: %+v", res)
	}
	if f.lastRef != "docker.io/acme/media-api:latest" {
		t.Fatalf("应使用 RefFor 的引用查询，实际 %q", f.lastRef)
	}
}

func TestCheckSingleImageNoTagRefForSameDigest(t *testing.T) {
	f := &fakeInspector{digest: map[string]string{
		"docker.io/acme/media-api:latest": "sha256:same",
	}}
	i := NewImageCheck()
	img := types.Image{
		Summary:   image.Summary{RepoDigests: []string{"acme/media-api@sha256:same"}},
		ImageName: "acme/media-api",
		ImageTag:  "None",
	}
	res := i.checkSingleImage(f, img, CheckUpdateDeps{RefFor: func(string) string { return "acme/media-api:latest" }})
	if res == nil || res.NeedUpdate || res.Uncheckable {
		t.Fatalf("digest 相同应为已是最新，得到 %+v", res)
	}
}

func TestCheckSingleImageForwardsAuth(t *testing.T) {
	f := &fakeInspector{digest: map[string]string{
		"ghcr.io/o/private:latest": "sha256:d",
	}}
	i := NewImageCheck()
	auth := &stubAuth{daemon: "ENCODED-AUTH", basic: "BASIC"}
	img := types.Image{
		Summary: image.Summary{
			RepoTags:    []string{"ghcr.io/o/private:latest"},
			RepoDigests: []string{"ghcr.io/o/private@sha256:d"},
		},
		ImageName: "ghcr.io/o/private",
		ImageTag:  "latest",
	}
	res := i.checkSingleImage(f, img, CheckUpdateDeps{Auth: auth})
	if res == nil || res.NeedUpdate {
		t.Fatalf("相同 digest 应为已是最新，得到 %+v", res)
	}
	if f.auths["ghcr.io/o/private:latest"] != "ENCODED-AUTH" {
		t.Fatalf("凭据未传给 DistributionInspect: %q", f.auths["ghcr.io/o/private:latest"])
	}
}

type stubAuth struct {
	daemon, basic string
	has           bool
}

func (s *stubAuth) DaemonAuth(string) string { return s.daemon }
func (s *stubAuth) BasicAuth(string) string  { return s.basic }
func (s *stubAuth) HasAuth(string) bool      { return s.has }

func TestCheckSingleImageLocalBuildWithoutRepoDigest(t *testing.T) {
	f := &fakeInspector{errs: map[string]error{
		"dockhamster:dev": errors.New("unauthorized: authentication required"),
	}}
	i := NewImageCheck()
	img := types.Image{
		Summary:   image.Summary{RepoTags: []string{"dockhamster:dev"}},
		ImageName: "dockhamster",
		ImageTag:  "dev",
	}
	res := i.checkSingleImage(f, img, CheckUpdateDeps{})
	if res == nil || !res.Uncheckable || res.Reason != CheckReasonNoTag {
		t.Fatalf("无 RepoDigest 的本地构建应标记 no-tag，得到 %+v", res)
	}
	if f.lastRef != "" {
		t.Fatalf("本地构建不应去查 registry，实际查了 %q", f.lastRef)
	}
}

func TestCheckSingleImageAuthBadWhenCredsConfigured(t *testing.T) {
	f := &fakeInspector{errs: map[string]error{
		"ghcr.io/o/private:latest": errors.New("unauthorized"),
	}}
	i := NewImageCheck()
	img := types.Image{
		Summary: image.Summary{
			RepoTags:    []string{"ghcr.io/o/private:latest"},
			RepoDigests: []string{"ghcr.io/o/private@sha256:old"},
		},
		ImageName: "ghcr.io/o/private",
		ImageTag:  "latest",
	}
	res := i.checkSingleImage(f, img, CheckUpdateDeps{Auth: &stubAuth{has: true, daemon: "X"}})
	if res == nil || res.Reason != CheckReasonAuthBad {
		t.Fatalf("配了凭据仍被拒应为 auth-bad，得到 %+v", res)
	}
}

func TestCheckErrorReason(t *testing.T) {
	cases := []struct {
		msg  string
		want string
	}{
		{"Head https://ghcr.io/v2/x/manifests/latest: unauthorized", CheckReasonAuth},
		{"pull access denied for x", CheckReasonAuth},
		{"manifest unknown", CheckReasonNotFound},
		{"repository does not exist", CheckReasonNotFound},
		{"dial tcp 1.2.3.4:443: i/o timeout", ""},
		{"TLS handshake timeout", ""},
		{"dial tcp: lookup registry.example: no such host", ""}, // DNS 失败是暂态，别误判永久
	}
	for _, c := range cases {
		if got := checkErrorReason(errors.New(c.msg)); got != c.want {
			t.Errorf("checkErrorReason(%q) = %q, want %q", c.msg, got, c.want)
		}
	}
}
