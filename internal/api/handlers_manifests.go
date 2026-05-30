package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type applyManifestRequest struct {
	Manifest string `json:"manifest"`
}

type scanManifestImagesRequest struct {
	Manifest string `json:"manifest"`
}

func (s *Server) handleApplyManifest(w http.ResponseWriter, r *http.Request) {
	var req applyManifestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	applied, err := s.kv().ApplyManifest(ctx, req.Manifest)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"resources": applied})
}

func (s *Server) handleScanManifestImages(w http.ResponseWriter, r *http.Request) {
	var req scanManifestImagesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	images, err := extractManifestImages(req.Manifest)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"images": images})
}

func extractManifestImages(manifest string) ([]string, error) {
	dec := yaml.NewDecoder(strings.NewReader(manifest))
	seen := map[string]bool{}
	var images []string
	for {
		var doc yaml.Node
		if err := dec.Decode(&doc); err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
		collectManifestImages(&doc, seen, &images)
	}
	return images, nil
}

func collectManifestImages(node *yaml.Node, seen map[string]bool, images *[]string) {
	if node == nil {
		return
	}
	switch node.Kind {
	case yaml.DocumentNode, yaml.SequenceNode:
		for _, child := range node.Content {
			collectManifestImages(child, seen, images)
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			key := node.Content[i]
			value := node.Content[i+1]
			if key.Value == "image" && value.Kind == yaml.ScalarNode {
				ref := strings.TrimSpace(value.Value)
				if ref != "" && !seen[ref] {
					seen[ref] = true
					*images = append(*images, ref)
				}
			}
			collectManifestImages(value, seen, images)
		}
	}
}
