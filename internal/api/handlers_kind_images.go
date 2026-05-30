package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"sort"
	"strings"
	"time"
)

const (
	ociImageIndexMediaType  = "application/vnd.oci.image.index.v1+json"
	dockerManifestListMedia = "application/vnd.docker.distribution.manifest.list.v2+json"
)

type kindDockerImageStatus struct {
	Reference    string   `json:"reference"`
	ID           string   `json:"id,omitempty"`
	Size         string   `json:"size,omitempty"`
	CreatedSince string   `json:"created_since,omitempty"`
	Loaded       bool     `json:"loaded"`
	LoadedNodes  []string `json:"loaded_nodes,omitempty"`
	MissingNodes []string `json:"missing_nodes,omitempty"`
}

type kindImageLoadRequest struct {
	Image string `json:"image"`
}

func (s *Server) handleKindImageCache(w http.ResponseWriter, r *http.Request) {
	clusterName, err := s.activeKindClusterName()
	if err != nil {
		writeError(w, http.StatusPreconditionFailed, err.Error())
		return
	}
	images, err := listDockerImages(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	nodes, nodeImages, err := listKindNodeImages(r.Context(), clusterName)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for i := range images {
		candidates := imageReferenceCandidates(images[i].Reference)
		for _, node := range nodes {
			if nodeHasAnyImageRef(nodeImages[node], candidates) {
				images[i].LoadedNodes = append(images[i].LoadedNodes, node)
			} else {
				images[i].MissingNodes = append(images[i].MissingNodes, node)
			}
		}
		images[i].Loaded = len(nodes) > 0 && len(images[i].MissingNodes) == 0
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"cluster": clusterName,
		"context": s.clusters.ActiveContext(),
		"nodes":   nodes,
		"images":  images,
	})
}

func (s *Server) handleKindImageLoad(w http.ResponseWriter, r *http.Request) {
	clusterName, err := s.activeKindClusterName()
	if err != nil {
		writeError(w, http.StatusPreconditionFailed, err.Error())
		return
	}
	if !kindAvailable() {
		writeError(w, http.StatusPreconditionFailed, "kind CLI not found on PATH")
		return
	}
	var req kindImageLoadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	image := strings.TrimSpace(req.Image)
	if image == "" || strings.ContainsAny(image, "\r\n\t") {
		writeError(w, http.StatusBadRequest, "image is required")
		return
	}
	if !dockerImageExists(r.Context(), image) {
		writeError(w, http.StatusNotFound, "image not found in local Docker cache")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}
	setSSEHeaders(w, flusher)
	streamPhase(w, flusher, "Loading "+image+" into KinD cluster "+clusterName)
	if err := loadDockerImageIntoKind(r.Context(), w, flusher, clusterName, image); err != nil {
		writeClusterSSE(w, flusher, clusterSSEEvent{Type: "error", Error: err.Error()})
		return
	}
	writeClusterSSE(w, flusher, clusterSSEEvent{Type: "done", Context: s.clusters.ActiveContext()})
}

func (s *Server) activeKindClusterName() (string, error) {
	if s.clusters.InCluster() {
		return "", fmt.Errorf("kind image cache is disabled when running in-cluster")
	}
	ctx := s.clusters.ActiveContext()
	if !strings.HasPrefix(ctx, "kind-") {
		return "", fmt.Errorf("active context %q is not a KinD cluster", ctx)
	}
	return strings.TrimPrefix(ctx, "kind-"), nil
}

func nodeHasAnyImageRef(refs map[string]bool, candidates []string) bool {
	for _, ref := range candidates {
		if refs[ref] {
			return true
		}
	}
	return false
}

func imageReferenceCandidates(ref string) []string {
	out := []string{ref}
	repo, tag, ok := strings.Cut(ref, ":")
	if !ok || strings.Contains(repo, ".") || strings.Contains(repo, ":") || strings.HasPrefix(repo, "localhost/") {
		return out
	}
	if !strings.Contains(repo, "/") {
		out = append(out, "docker.io/library/"+repo+":"+tag)
	} else {
		out = append(out, "docker.io/"+repo+":"+tag)
	}
	return out
}

func loadDockerImageIntoKind(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, clusterName, image string) error {
	corrected, changed, err := correctDockerImageForKindNode(ctx, w, flusher, clusterName, image)
	if err != nil {
		streamLine(w, flusher, "  warning: image auto-correct skipped: "+err.Error())
	}
	if changed {
		streamLine(w, flusher, "  using single-platform local image "+corrected)
	}
	return streamCommand(ctx, w, flusher, "kind", "load", "docker-image", "--name", clusterName, corrected)
}

func correctDockerImageForKindNode(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, clusterName, image string) (string, bool, error) {
	mediaType, err := localDockerImageMediaType(ctx, image)
	if err != nil {
		return image, false, err
	}
	if mediaType != ociImageIndexMediaType && mediaType != dockerManifestListMedia {
		return image, false, nil
	}

	platform, err := kindNodePlatform(ctx, clusterName)
	if err != nil {
		return image, false, err
	}
	digest, err := imageManifestDigestForPlatform(ctx, image, platform.os, platform.arch, platform.variant)
	if err != nil {
		return image, false, err
	}

	repo := dockerImageRepository(image)
	if repo == "" {
		return image, false, fmt.Errorf("could not derive repository from %q", image)
	}
	childRef := repo + "@" + digest
	streamLine(w, flusher, fmt.Sprintf("  %s is a multi-platform index; selecting %s for %s/%s", image, digest, platform.os, platform.arch))
	if err := exec.CommandContext(ctx, "docker", "pull", childRef).Run(); err != nil {
		return image, false, fmt.Errorf("pull platform manifest %s: %w", childRef, err)
	}
	if err := exec.CommandContext(ctx, "docker", "tag", childRef, image).Run(); err != nil {
		return image, false, fmt.Errorf("retag %s as %s: %w", childRef, image, err)
	}
	return image, true, nil
}

type dockerPlatform struct {
	os      string
	arch    string
	variant string
}

func localDockerImageMediaType(ctx context.Context, image string) (string, error) {
	out, err := exec.CommandContext(ctx, "docker", "image", "inspect", image).Output()
	if err != nil {
		return "", fmt.Errorf("inspect local image %s: %w", image, err)
	}
	var body []struct {
		Descriptor struct {
			MediaType string `json:"mediaType"`
		} `json:"Descriptor"`
	}
	if err := json.Unmarshal(out, &body); err != nil {
		return "", fmt.Errorf("parse docker image inspect: %w", err)
	}
	if len(body) == 0 {
		return "", fmt.Errorf("image %s not found", image)
	}
	return body[0].Descriptor.MediaType, nil
}

func kindNodePlatform(ctx context.Context, clusterName string) (dockerPlatform, error) {
	node := clusterName + "-control-plane"
	out, err := exec.CommandContext(ctx, "docker", "exec", node, "uname", "-m").Output()
	if err != nil {
		return dockerPlatform{}, fmt.Errorf("detect KinD node architecture: %w", err)
	}
	arch, variant, err := dockerArchFromUname(strings.TrimSpace(string(out)))
	if err != nil {
		return dockerPlatform{}, err
	}
	return dockerPlatform{os: "linux", arch: arch, variant: variant}, nil
}

func dockerArchFromUname(uname string) (arch, variant string, err error) {
	switch uname {
	case "x86_64", "amd64":
		return "amd64", "", nil
	case "aarch64", "arm64":
		return "arm64", "v8", nil
	case "s390x":
		return "s390x", "", nil
	case "ppc64le":
		return "ppc64le", "", nil
	case "armv7l":
		return "arm", "v7", nil
	case "armv6l":
		return "arm", "v6", nil
	default:
		return "", "", fmt.Errorf("unsupported KinD node architecture %q", uname)
	}
}

func imageManifestDigestForPlatform(ctx context.Context, image, osName, arch, variant string) (string, error) {
	out, err := exec.CommandContext(ctx, "docker", "manifest", "inspect", image).Output()
	if err != nil {
		return "", fmt.Errorf("inspect manifest %s: %w", image, err)
	}
	var body struct {
		Manifests []struct {
			MediaType string `json:"mediaType"`
			Digest    string `json:"digest"`
			Platform  struct {
				Architecture string `json:"architecture"`
				OS           string `json:"os"`
				Variant      string `json:"variant"`
			} `json:"platform"`
		} `json:"manifests"`
	}
	if err := json.Unmarshal(out, &body); err != nil {
		return "", fmt.Errorf("parse docker manifest inspect: %w", err)
	}
	for _, manifest := range body.Manifests {
		if manifest.Platform.OS != osName || manifest.Platform.Architecture != arch {
			continue
		}
		if variant != "" && manifest.Platform.Variant != "" && manifest.Platform.Variant != variant {
			continue
		}
		if manifest.Digest == "" {
			return "", fmt.Errorf("manifest for %s/%s has no digest", osName, arch)
		}
		return manifest.Digest, nil
	}
	return "", fmt.Errorf("no manifest found for %s/%s in %s", osName, arch, image)
}

func dockerImageRepository(ref string) string {
	if repo, _, ok := strings.Cut(ref, "@"); ok {
		return repo
	}
	slash := strings.LastIndex(ref, "/")
	colon := strings.LastIndex(ref, ":")
	if colon > slash {
		return ref[:colon]
	}
	return ref
}

func listDockerImages(ctx context.Context) ([]kindDockerImageStatus, error) {
	if _, err := exec.LookPath("docker"); err != nil {
		return nil, fmt.Errorf("docker CLI not found on PATH")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", "image", "ls", "--format", "{{.Repository}}\t{{.Tag}}\t{{.ID}}\t{{.Size}}\t{{.CreatedSince}}").Output()
	if err != nil {
		return nil, fmt.Errorf("list docker images: %w", err)
	}
	var images []kindDockerImageStatus
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 5 || parts[0] == "<none>" || parts[1] == "<none>" {
			continue
		}
		ref := parts[0] + ":" + parts[1]
		if seen[ref] {
			continue
		}
		seen[ref] = true
		images = append(images, kindDockerImageStatus{
			Reference:    ref,
			ID:           parts[2],
			Size:         parts[3],
			CreatedSince: parts[4],
		})
	}
	sort.Slice(images, func(i, j int) bool { return images[i].Reference < images[j].Reference })
	return images, nil
}

func listKindNodeImages(ctx context.Context, clusterName string) ([]string, map[string]map[string]bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", "ps", "--filter", "label=io.x-k8s.kind.cluster="+clusterName, "--format", "{{.Names}}").Output()
	if err != nil {
		return nil, nil, fmt.Errorf("list kind nodes: %w", err)
	}
	var nodes []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if name := strings.TrimSpace(line); name != "" {
			nodes = append(nodes, name)
		}
	}
	sort.Strings(nodes)
	byNode := make(map[string]map[string]bool, len(nodes))
	for _, node := range nodes {
		refs, err := listKindNodeImageRefs(ctx, node)
		if err != nil {
			return nil, nil, err
		}
		byNode[node] = refs
	}
	return nodes, byNode, nil
}

func listKindNodeImageRefs(ctx context.Context, node string) (map[string]bool, error) {
	out, err := exec.CommandContext(ctx, "docker", "exec", node, "crictl", "images", "-o", "json").Output()
	if err != nil {
		return nil, fmt.Errorf("list images in kind node %s: %w", node, err)
	}
	var body struct {
		Images []struct {
			RepoTags []string `json:"repoTags"`
		} `json:"images"`
	}
	if err := json.Unmarshal(out, &body); err != nil {
		return nil, fmt.Errorf("parse images in kind node %s: %w", node, err)
	}
	refs := map[string]bool{}
	for _, img := range body.Images {
		for _, tag := range img.RepoTags {
			if tag != "" && tag != "<none>:<none>" {
				refs[tag] = true
			}
		}
	}
	return refs, nil
}
