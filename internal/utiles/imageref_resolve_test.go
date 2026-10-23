package utiles

import "testing"

func TestIsBareImageID(t *testing.T) {
	yes := []string{
		"sha256:a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90",
		"a1b2c3d4e5f6", // 12 位短 ID
		"SHA256:A1B2C3D4E5F60718293A4B5C6D7E8F90A1B2C3D4E5F60718293A4B5C6D7E8F90",
	}
	no := []string{
		"nginx:latest",
		"acme/media-api:latest",
		"ghcr.io/libremk66/hamsterdash:latest",
		"115lite:latest",
		"",
		"sha256:xyz",
	}
	for _, s := range yes {
		if !IsBareImageID(s) {
			t.Errorf("IsBareImageID(%q) = false, want true", s)
		}
	}
	for _, s := range no {
		if IsBareImageID(s) {
			t.Errorf("IsBareImageID(%q) = true, want false", s)
		}
	}
}

func TestPullableImageRef(t *testing.T) {
	cases := []struct {
		name        string
		listImage   string
		configImage string
		want        string
	}{
		{"list 正常", "acme/media-api:latest", "docker.io/acme/media-api:latest", "docker.io/acme/media-api:latest"},
		{"list 是裸 ID → 用 config", "sha256:a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90", "registry.example.com/team/app:latest", "registry.example.com/team/app:latest"},
		{"list 为空 → 用 config", "", "postgres:16-alpine", "docker.io/library/postgres:16-alpine"},
		{"两个都是裸 ID → 空", "sha256:0f1e2d3c4b5a69788796a5b4c3d2e1f00f1e2d3c4b5a69788796a5b4c3d2e1f0", "sha256:0f1e2d3c4b5a69788796a5b4c3d2e1f00f1e2d3c4b5a69788796a5b4c3d2e1f0", ""},
		{"两个都空 → 空", "", "", ""},
		{"list 无 tag → 补 latest", "nginx", "", "docker.io/library/nginx:latest"},
		{"list 非法 → 用 config", "bad ref!!", "nginx:1.25", "docker.io/library/nginx:1.25"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := PullableImageRef(c.listImage, c.configImage); got != c.want {
				t.Fatalf("PullableImageRef(%q, %q) = %q, want %q", c.listImage, c.configImage, got, c.want)
			}
		})
	}
}
