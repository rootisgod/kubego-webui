package kubevirt

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Image uploads go through CDI: a DataVolume with `source: upload`
// provisions a backing PVC, CDI signs an upload token, the bytes are
// streamed to the cdi-uploadproxy Service via the apiserver proxy so
// KubeGo stays reachable from outside the cluster network.
const (
	imageUploadLabel          = "kubego.io/image-upload"
	imageUploadNameAnnotation = "kubego.io/image-name"
	imageUploadKindAnnotation = "kubego.io/image-kind" // "iso" | "disk"

	cdiUploadProxyNamespace = "cdi"
	cdiUploadProxyService   = "cdi-uploadproxy"
	cdiUploadProxyPort      = "443"
)

var (
	dataVolumeGVR = schema.GroupVersionResource{
		Group:    "cdi.kubevirt.io",
		Version:  "v1beta1",
		Resource: "datavolumes",
	}
	uploadTokenRequestPath = func(namespace string) string {
		return fmt.Sprintf("/apis/upload.cdi.kubevirt.io/v1beta1/namespaces/%s/uploadtokenrequests", namespace)
	}
	uploadProxyPath = fmt.Sprintf("/api/v1/namespaces/%s/services/https:%s:%s/proxy/v1beta1/upload", cdiUploadProxyNamespace, cdiUploadProxyService, cdiUploadProxyPort)
)

// ImageUpload is the wire shape of a managed image. Backed 1:1 by a
// CDI DataVolume labelled `kubego.io/image-upload=true`.
type ImageUpload struct {
	Name    string `json:"name"`         // user-facing display name
	PVCName string `json:"pvc_name"`     // underlying PVC (== DV name)
	Kind    string `json:"kind"`         // "iso" | "disk"
	Phase   string `json:"phase"`        // mirror of DV status.phase
	Size    string `json:"size"`         // human-readable requested storage
	Created string `json:"created"`
}

// ListImageUploads returns the KubeGo-managed image uploads in the driver's
// namespace. We filter by the `kubego.io/image-upload=true` label so a
// CDI DataVolume created by a VM launch (root disk) does not show up here.
func (c *kubevirtClient) ListImageUploads() ([]ImageUpload, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	list, err := c.dyn.Resource(dataVolumeGVR).Namespace(c.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: imageUploadLabel + "=true",
	})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return []ImageUpload{}, nil
		}
		return nil, fmt.Errorf("list data volumes: %w", err)
	}
	out := make([]ImageUpload, 0, len(list.Items))
	for i := range list.Items {
		dv := &list.Items[i]
		ann := dv.GetAnnotations()
		name := ann[imageUploadNameAnnotation]
		if name == "" {
			name = dv.GetName()
		}
		phase, _, _ := unstructured.NestedString(dv.Object, "status", "phase")
		size, _, _ := unstructured.NestedString(dv.Object, "spec", "storage", "resources", "requests", "storage")
		out = append(out, ImageUpload{
			Name:    name,
			PVCName: dv.GetName(),
			Kind:    ann[imageUploadKindAnnotation],
			Phase:   phase,
			Size:    size,
			Created: dv.GetCreationTimestamp().UTC().Format(time.RFC3339),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// CreateImageUpload provisions a blank DataVolume sized to hold an upload.
// The caller then invokes UploadImageBytes to stream the payload. Split
// into two calls so progress can be driven by the HTTP layer — the DV
// must exist before CDI will mint an upload token.
func (c *kubevirtClient) CreateImageUpload(pvcName, displayName, kind string, sizeGB int) error {
	if pvcName == "" {
		return fmt.Errorf("pvc name is required")
	}
	if !pvcNamePattern.MatchString(pvcName) {
		return fmt.Errorf("invalid pvc name: must be a lowercase DNS label (letters, digits, hyphens)")
	}
	if sizeGB < 1 {
		return fmt.Errorf("size must be at least 1 GB")
	}
	if kind == "" {
		kind = "iso"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	dv := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": dataVolumeGVR.Group + "/" + dataVolumeGVR.Version,
		"kind":       "DataVolume",
		"metadata": map[string]any{
			"name":      pvcName,
			"namespace": c.namespace,
			"labels": map[string]any{
				imageUploadLabel:               "true",
				"app.kubernetes.io/managed-by": "kubego",
			},
			"annotations": map[string]any{
				imageUploadNameAnnotation: displayName,
				imageUploadKindAnnotation: kind,
				// Skip CDI's automatic "bind immediate" behavior so the DV
				// sits in UploadReady instead of WaitForFirstConsumer on
				// storage classes with WaitForFirstConsumer binding mode.
				"cdi.kubevirt.io/storage.bind.immediate.requested": "true",
			},
		},
		"spec": map[string]any{
			"source": map[string]any{
				"upload": map[string]any{},
			},
			"storage": map[string]any{
				"accessModes": []any{"ReadWriteOnce"},
				"volumeMode":  "Filesystem",
				"resources": map[string]any{
					"requests": map[string]any{
						"storage": fmt.Sprintf("%dGi", sizeGB),
					},
				},
			},
		},
	}}

	_, err := c.dyn.Resource(dataVolumeGVR).Namespace(c.namespace).Create(ctx, dv, metav1.CreateOptions{})
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("image %q already exists", pvcName)
		}
		return fmt.Errorf("create DataVolume: %w", err)
	}
	return nil
}

// CreateImageImport provisions a DataVolume that pulls its contents from
// `sourceURL` via CDI's importer pod (`source: http`). Unlike the upload
// flow, this is fire-and-forget: CDI runs the import asynchronously and
// the DV phase progresses ImportScheduled → ImportInProgress → Succeeded.
// The PVC size must be large enough to hold the fetched payload plus
// CDI's overhead — callers should size generously since the importer
// will fail with "no space left" rather than auto-resize.
func (c *kubevirtClient) CreateImageImport(pvcName, displayName, kind string, sizeGB int, sourceURL string) error {
	if pvcName == "" {
		return fmt.Errorf("pvc name is required")
	}
	if !pvcNamePattern.MatchString(pvcName) {
		return fmt.Errorf("invalid pvc name: must be a lowercase DNS label (letters, digits, hyphens)")
	}
	if sizeGB < 1 {
		return fmt.Errorf("size must be at least 1 GB")
	}
	if kind == "" {
		kind = "iso"
	}
	sourceURL = strings.TrimSpace(sourceURL)
	if sourceURL == "" {
		return fmt.Errorf("source url is required")
	}
	if !strings.HasPrefix(sourceURL, "http://") && !strings.HasPrefix(sourceURL, "https://") {
		return fmt.Errorf("source url must start with http:// or https://")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	dv := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": dataVolumeGVR.Group + "/" + dataVolumeGVR.Version,
		"kind":       "DataVolume",
		"metadata": map[string]any{
			"name":      pvcName,
			"namespace": c.namespace,
			"labels": map[string]any{
				imageUploadLabel:               "true",
				"app.kubernetes.io/managed-by": "kubego",
			},
			"annotations": map[string]any{
				imageUploadNameAnnotation: displayName,
				imageUploadKindAnnotation: kind,
				"cdi.kubevirt.io/storage.bind.immediate.requested": "true",
			},
		},
		"spec": map[string]any{
			"source": map[string]any{
				"http": map[string]any{
					"url": sourceURL,
				},
			},
			"storage": map[string]any{
				"accessModes": []any{"ReadWriteOnce"},
				"volumeMode":  "Filesystem",
				"resources": map[string]any{
					"requests": map[string]any{
						"storage": fmt.Sprintf("%dGi", sizeGB),
					},
				},
			},
		},
	}}

	_, err := c.dyn.Resource(dataVolumeGVR).Namespace(c.namespace).Create(ctx, dv, metav1.CreateOptions{})
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("image %q already exists", pvcName)
		}
		return fmt.Errorf("create DataVolume: %w", err)
	}
	return nil
}

// UploadImageBytes streams `contentLength` bytes from `body` into the
// previously-created DataVolume. The flow is: wait for UploadReady,
// request a token from CDI, POST the bytes to cdi-uploadproxy via the
// apiserver service-proxy so KubeGo does not need direct pod-network
// connectivity. Blocks until upload completes or ctx is cancelled.
func (c *kubevirtClient) UploadImageBytes(ctx context.Context, pvcName string, body io.Reader, contentLength int64) error {
	if pvcName == "" {
		return fmt.Errorf("pvc name is required")
	}

	if err := c.waitForUploadReady(ctx, pvcName); err != nil {
		return err
	}

	token, err := c.requestUploadToken(ctx, pvcName)
	if err != nil {
		return fmt.Errorf("request upload token: %w", err)
	}

	req := c.kube.CoreV1().RESTClient().Post().
		AbsPath(uploadProxyPath).
		SetHeader("Authorization", "Bearer "+token).
		SetHeader("Content-Type", "application/octet-stream").
		Body(body)
	if contentLength > 0 {
		req = req.SetHeader("Content-Length", fmt.Sprintf("%d", contentLength))
	}
	resp := req.Do(ctx)
	if err := resp.Error(); err != nil {
		// REST client wraps the HTTP body in the error when it's a
		// non-2xx. Surface it so the UI can show a real reason.
		return fmt.Errorf("upload to cdi-uploadproxy: %w", err)
	}
	return nil
}

// waitForUploadReady polls the DV until status.phase is UploadReady or a
// terminal non-ready state. CDI 1.x uses UploadReady; older 1.x versions
// also surface PendingPopulation before UploadReady — we accept either.
func (c *kubevirtClient) waitForUploadReady(ctx context.Context, pvcName string) error {
	deadline := time.Now().Add(2 * time.Minute)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for CDI to reach UploadReady for %q", pvcName)
		}
		dv, err := c.dyn.Resource(dataVolumeGVR).Namespace(c.namespace).Get(ctx, pvcName, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("get DataVolume: %w", err)
		}
		phase, _, _ := unstructured.NestedString(dv.Object, "status", "phase")
		switch phase {
		case "UploadReady", "UploadScheduled":
			return nil
		case "Succeeded":
			return fmt.Errorf("upload for %q has already completed — delete and recreate to re-upload", pvcName)
		case "Failed":
			return fmt.Errorf("DataVolume %q is in Failed state", pvcName)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// requestUploadToken asks CDI for a short-lived token that lets us POST
// bytes to cdi-uploadproxy on behalf of a specific PVC. The endpoint is
// one-shot — the resource exists only long enough to echo the token back.
func (c *kubevirtClient) requestUploadToken(ctx context.Context, pvcName string) (string, error) {
	body := map[string]any{
		"apiVersion": "upload.cdi.kubevirt.io/v1beta1",
		"kind":       "UploadTokenRequest",
		"metadata": map[string]any{
			"name":      pvcName,
			"namespace": c.namespace,
		},
		"spec": map[string]any{
			"pvcName": pvcName,
		},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	resp, err := c.kube.CoreV1().RESTClient().Post().
		AbsPath(uploadTokenRequestPath(c.namespace)).
		SetHeader("Content-Type", "application/json").
		Body(raw).
		DoRaw(ctx)
	if err != nil {
		return "", err
	}
	var decoded struct {
		Status struct {
			Token string `json:"token"`
		} `json:"status"`
	}
	if err := json.Unmarshal(resp, &decoded); err != nil {
		return "", fmt.Errorf("parse UploadTokenRequest response: %w", err)
	}
	if decoded.Status.Token == "" {
		return "", fmt.Errorf("empty token in UploadTokenRequest response")
	}
	return decoded.Status.Token, nil
}

// DeleteImageUpload removes the DataVolume (and via CDI's owner refs
// the backing PVC). Safe to call on an incomplete upload — CDI's
// garbage collector handles intermediate state.
func (c *kubevirtClient) DeleteImageUpload(pvcName string) error {
	if pvcName == "" {
		return fmt.Errorf("pvc name is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	err := c.dyn.Resource(dataVolumeGVR).Namespace(c.namespace).Delete(ctx, pvcName, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete DataVolume: %w", err)
	}
	return nil
}

// pvcNamePattern is a DNS-label regex; PVCs inherit the Kubernetes
// resource-name rules regardless of what display name the user picked.
var pvcNamePattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// ImageUploadPVCName returns a DNS-label version of `display` by
// lowercasing, replacing invalid chars with `-`, and prefixing "img-".
// Collisions surface as AlreadyExists from CreateImageUpload.
func ImageUploadPVCName(display string) string {
	display = strings.ToLower(strings.TrimSpace(display))
	var b strings.Builder
	b.WriteString("img-")
	for _, r := range display {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == ' ' || r == '.':
			b.WriteRune('-')
		}
	}
	out := b.String()
	if len(out) > 63 {
		out = out[:63]
	}
	return strings.TrimRight(out, "-")
}
