package api

import (
	"reflect"
	"testing"
)

func TestExtractManifestImages(t *testing.T) {
	manifest := `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
spec:
  template:
    spec:
      initContainers:
        - name: setup
          image: busybox:1.36
      containers:
        - name: app
          image: nginx:1.27
        - name: sidecar
          image: quay.io/example/sidecar:v2
---
apiVersion: batch/v1
kind: Job
metadata:
  name: worker
spec:
  template:
    spec:
      containers:
        - name: worker
          image: nginx:1.27
`

	got, err := extractManifestImages(manifest)
	if err != nil {
		t.Fatalf("extractManifestImages returned error: %v", err)
	}
	want := []string{"busybox:1.36", "nginx:1.27", "quay.io/example/sidecar:v2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("extractManifestImages() = %#v; want %#v", got, want)
	}
}

func TestExtractManifestImagesRejectsInvalidYAML(t *testing.T) {
	if _, err := extractManifestImages("spec:\n  containers:\n    - image: ["); err == nil {
		t.Fatal("expected invalid YAML error")
	}
}
