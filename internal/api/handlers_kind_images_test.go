package api

import "testing"

func TestDockerArchFromUname(t *testing.T) {
	tests := []struct {
		name        string
		uname       string
		wantArch    string
		wantVariant string
		wantErr     bool
	}{
		{name: "amd64", uname: "x86_64", wantArch: "amd64"},
		{name: "arm64", uname: "aarch64", wantArch: "arm64", wantVariant: "v8"},
		{name: "armv7", uname: "armv7l", wantArch: "arm", wantVariant: "v7"},
		{name: "unknown", uname: "mips64", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			arch, variant, err := dockerArchFromUname(tt.uname)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if arch != tt.wantArch || variant != tt.wantVariant {
				t.Fatalf("dockerArchFromUname(%q) = %q, %q; want %q, %q", tt.uname, arch, variant, tt.wantArch, tt.wantVariant)
			}
		})
	}
}

func TestDockerImageRepository(t *testing.T) {
	tests := map[string]string{
		"ubuntu:24.04": "ubuntu",
		"quay.io/kubevirt/virt-controller:v1.8.2":           "quay.io/kubevirt/virt-controller",
		"localhost:5000/example/image:dev":                  "localhost:5000/example/image",
		"quay.io/kubevirt/virt-controller@sha256:abcdef123": "quay.io/kubevirt/virt-controller",
		"busybox": "busybox",
	}

	for ref, want := range tests {
		if got := dockerImageRepository(ref); got != want {
			t.Fatalf("dockerImageRepository(%q) = %q; want %q", ref, got, want)
		}
	}
}

func TestNormalizeImagePullRefs(t *testing.T) {
	got, err := normalizeImagePullRefs(
		[]string{"nginx:1.27", "busybox:1.36"},
		"nginx:1.27, quay.io/example/app:v1\nredis:7",
	)
	if err != nil {
		t.Fatalf("normalizeImagePullRefs returned error: %v", err)
	}
	want := []string{"nginx:1.27", "busybox:1.36", "quay.io/example/app:v1", "redis:7"}
	if len(got) != len(want) {
		t.Fatalf("normalizeImagePullRefs() = %#v; want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("normalizeImagePullRefs() = %#v; want %#v", got, want)
		}
	}
}

func TestNormalizeImagePullRefsRejectsWhitespace(t *testing.T) {
	if _, err := normalizeImagePullRefs(nil, "nginx:1.27 bad ref:latest"); err == nil {
		t.Fatal("expected whitespace error")
	}
}
