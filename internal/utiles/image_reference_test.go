package utiles

import "testing"

func TestSplitTaggedReference(t *testing.T) {
	for _, tc := range []struct{ ref, name, tag string }{
		{"registry.example:5000/team/app:latest", "registry.example:5000/team/app", "latest"},
		{"registry.example:5000/team/app", "registry.example:5000/team/app", "latest"},
		{"nginx:alpine", "nginx", "alpine"},
		{"nginx", "nginx", "latest"},
		{"example/app@sha256:abcd", "example/app", "None"},
	} {
		t.Run(tc.ref, func(t *testing.T) {
			name, tag := splitTaggedReference(tc.ref)
			if name != tc.name || tag != tc.tag {
				t.Fatalf("got %q:%q, want %q:%q", name, tag, tc.name, tc.tag)
			}
		})
	}
}
