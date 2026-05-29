package kubevirt

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/restmapper"
)

type AppliedResource struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Namespace  string `json:"namespace,omitempty"`
	Name       string `json:"name"`
	Action     string `json:"action"`
}

func (c *kubevirtClient) ApplyManifest(ctx context.Context, manifest string) ([]AppliedResource, error) {
	if strings.TrimSpace(manifest) == "" {
		return nil, fmt.Errorf("manifest is empty")
	}

	mapper := restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(c.discovery))
	decoder := yaml.NewYAMLOrJSONDecoder(bytes.NewBufferString(manifest), 4096)
	var applied []AppliedResource

	for {
		var raw map[string]any
		if err := decoder.Decode(&raw); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("parse manifest: %w", err)
		}
		if len(raw) == 0 {
			continue
		}

		obj := &unstructured.Unstructured{Object: raw}
		if obj.GetAPIVersion() == "" || obj.GetKind() == "" {
			return nil, fmt.Errorf("manifest document is missing apiVersion or kind")
		}
		if obj.GetName() == "" {
			return nil, fmt.Errorf("%s/%s is missing metadata.name", obj.GetAPIVersion(), obj.GetKind())
		}

		gv, err := schema.ParseGroupVersion(obj.GetAPIVersion())
		if err != nil {
			return nil, fmt.Errorf("parse apiVersion for %s/%s: %w", obj.GetKind(), obj.GetName(), err)
		}
		mapping, err := mapper.RESTMapping(gv.WithKind(obj.GetKind()).GroupKind(), gv.Version)
		if err != nil {
			return nil, fmt.Errorf("map resource for %s/%s %s: %w", obj.GetKind(), obj.GetName(), obj.GetAPIVersion(), err)
		}

		namespace := obj.GetNamespace()
		if mapping.Scope.Name() == meta.RESTScopeNameNamespace && namespace == "" {
			namespace = c.namespace
			obj.SetNamespace(namespace)
		}

		data, err := json.Marshal(obj.Object)
		if err != nil {
			return nil, fmt.Errorf("encode %s/%s: %w", obj.GetKind(), obj.GetName(), err)
		}

		res := c.dyn.Resource(mapping.Resource)
		var ri dynamic.ResourceInterface
		if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
			ri = res.Namespace(namespace)
		} else {
			ri = res
		}

		_, getErr := ri.Get(ctx, obj.GetName(), metav1.GetOptions{})
		action := "configured"
		if errors.IsNotFound(getErr) {
			action = "created"
		} else if getErr != nil {
			return nil, fmt.Errorf("check existing %s/%s: %w", obj.GetKind(), obj.GetName(), getErr)
		}

		force := true
		if _, err := ri.Patch(ctx, obj.GetName(), types.ApplyPatchType, data, metav1.PatchOptions{
			FieldManager: "kubego-webui",
			Force:        &force,
		}); err != nil {
			return nil, fmt.Errorf("apply %s/%s: %w", obj.GetKind(), obj.GetName(), err)
		}

		applied = append(applied, AppliedResource{
			APIVersion: obj.GetAPIVersion(),
			Kind:       obj.GetKind(),
			Namespace:  namespace,
			Name:       obj.GetName(),
			Action:     action,
		})
	}

	if len(applied) == 0 {
		return nil, fmt.Errorf("manifest contains no resources")
	}
	return applied, nil
}
