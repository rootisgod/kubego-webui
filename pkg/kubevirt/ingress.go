package kubevirt

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// ingress-nginx is the baseline controller we target. The KubeGo install
// flow puts it in its own namespace with a well-known deployment name —
// stable enough to probe without having to interpret Helm values.
const (
	ingressControllerNamespace = "ingress-nginx"
	ingressControllerName      = "ingress-nginx-controller"
	ingressClassName           = "nginx"
	ingressLabelManaged        = "kubego.io/expose"
	ingressLabelVM             = "kubego.io/vm"
)

// IngressControllerStatus summarises whether ingress-nginx is installed
// and ready in the active cluster. Used by the UI to decide between
// showing an install prompt versus the expose UI.
type IngressControllerStatus struct {
	Installed bool   `json:"installed"`
	Ready     bool   `json:"ready"`
	Replicas  int32  `json:"replicas"`
	Version   string `json:"version,omitempty"`
	HostIP    string `json:"host_ip,omitempty"` // suggested node IP for nip.io hostnames
	HTTPPort  int    `json:"http_port,omitempty"`
}

// IngressInfo is one VM-port → Ingress exposure as the UI sees it.
type IngressInfo struct {
	ID         string `json:"id"`
	VM         string `json:"vm"`
	RemotePort int    `json:"remote_port"`
	Hostname   string `json:"hostname"`
	URL        string `json:"url"`
	ClassName  string `json:"class_name,omitempty"`
}

// IngressControllerStatus probes the cluster for a running ingress-nginx
// deployment. Missing namespace/deployment => Installed:false (not an
// error); any other apiserver error is returned.
func (c *kubevirtClient) IngressControllerStatus() (IngressControllerStatus, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	dep, err := c.kube.AppsV1().Deployments(ingressControllerNamespace).Get(ctx, ingressControllerName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return IngressControllerStatus{HostIP: firstNodeIP(ctx, c)}, nil
		}
		return IngressControllerStatus{}, fmt.Errorf("get ingress-nginx deployment: %w", err)
	}

	status := IngressControllerStatus{
		Installed: true,
		Replicas:  dep.Status.AvailableReplicas,
		HostIP:    firstNodeIP(ctx, c),
		HTTPPort:  firstNodeHTTPPort(ctx, c),
	}
	status.Ready = dep.Status.AvailableReplicas > 0 && dep.Status.UnavailableReplicas == 0
	// The container image tag is the pragmatic "version" signal —
	// ingress-nginx stamps no version into the deployment annotations.
	for _, con := range dep.Spec.Template.Spec.Containers {
		if con.Name == "controller" {
			if i := strings.LastIndex(con.Image, ":"); i > -1 {
				status.Version = con.Image[i+1:]
			}
			break
		}
	}
	return status, nil
}

// firstNodeIP returns an InternalIP from any control-plane node for use
// in nip.io hostnames. Callers accept an empty string (they render a
// "<ip>" placeholder rather than 500ing).
func firstNodeIP(ctx context.Context, c *kubevirtClient) string {
	n := firstIngressNode(ctx, c)
	if n == nil {
		return ""
	}
	for _, a := range n.Status.Addresses {
		if a.Type == corev1.NodeInternalIP && a.Address != "" {
			return a.Address
		}
	}
	return ""
}

func firstNodeHTTPPort(ctx context.Context, c *kubevirtClient) int {
	n := firstIngressNode(ctx, c)
	if n == nil {
		return 0
	}
	if raw := n.Labels["kubego.io/ingress-http-port"]; raw != "" {
		var port int
		if _, err := fmt.Sscanf(raw, "%d", &port); err == nil && port > 0 && port <= 65535 {
			return port
		}
	}
	return 80
}

func firstIngressNode(ctx context.Context, c *kubevirtClient) *corev1.Node {
	nodes, err := c.kube.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil
	}
	// Prefer control-plane; fall back to any node.
	pick := func(filter func(corev1.Node) bool) *corev1.Node {
		for i := range nodes.Items {
			if !filter(nodes.Items[i]) {
				continue
			}
			return &nodes.Items[i]
		}
		return nil
	}
	if n := pick(func(n corev1.Node) bool {
		_, ok1 := n.Labels["node-role.kubernetes.io/control-plane"]
		_, ok2 := n.Labels["node-role.kubernetes.io/master"]
		return ok1 || ok2
	}); n != nil {
		return n
	}
	return pick(func(n corev1.Node) bool { return true })
}

func ingressURL(hostname string, httpPort int) string {
	if httpPort <= 0 || httpPort == 80 {
		return "http://" + hostname + "/"
	}
	return fmt.Sprintf("http://%s:%d/", hostname, httpPort)
}

// ListVMIngresses returns the KubeGo-managed exposures for a VM. We
// filter by the `kubego.io/expose=true` label so hand-crafted Ingresses
// do not show up here and risk being deleted by the UI.
func (c *kubevirtClient) ListVMIngresses(vmName string) ([]IngressInfo, error) {
	if err := ValidateVMName(vmName); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	sel := fmt.Sprintf("%s=true,%s=%s", ingressLabelManaged, ingressLabelVM, vmName)
	ings, err := c.kube.NetworkingV1().Ingresses(c.namespace).List(ctx, metav1.ListOptions{LabelSelector: sel})
	if err != nil {
		return nil, fmt.Errorf("list ingresses: %w", err)
	}
	httpPort := firstNodeHTTPPort(ctx, c)
	out := make([]IngressInfo, 0, len(ings.Items))
	for i := range ings.Items {
		ing := &ings.Items[i]
		info := IngressInfo{
			ID: ing.Name,
			VM: vmName,
		}
		if ing.Spec.IngressClassName != nil {
			info.ClassName = *ing.Spec.IngressClassName
		}
		if len(ing.Spec.Rules) > 0 {
			info.Hostname = ing.Spec.Rules[0].Host
			if info.Hostname != "" {
				info.URL = ingressURL(info.Hostname, httpPort)
			}
			if ing.Spec.Rules[0].HTTP != nil && len(ing.Spec.Rules[0].HTTP.Paths) > 0 {
				if p := ing.Spec.Rules[0].HTTP.Paths[0].Backend.Service; p != nil {
					info.RemotePort = int(p.Port.Number)
				}
			}
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// ExposeVMPort creates a Service targeting the VM's virt-launcher pod
// and an Ingress with a nip.io hostname. Idempotent: calling with the
// same (vm, port) returns the existing exposure.
func (c *kubevirtClient) ExposeVMPort(vmName string, port int) (IngressInfo, error) {
	if err := ValidateVMName(vmName); err != nil {
		return IngressInfo{}, err
	}
	if port <= 0 || port > 65535 {
		return IngressInfo{}, fmt.Errorf("port must be 1..65535")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	hostIP := firstNodeIP(ctx, c)
	httpPort := firstNodeHTTPPort(ctx, c)
	if hostIP == "" {
		// The ingress record still resolves in some environments where
		// users override DNS; fall back to "localhost" which works for
		// KinD + port-mapping setups via /etc/hosts.
		hostIP = "127.0.0.1"
	}
	resourceName := fmt.Sprintf("%s-expose-%d", vmName, port)
	hostname := fmt.Sprintf("%s-%d.%s.nip.io", vmName, port, hostIP)

	labels := map[string]string{
		ingressLabelManaged:            "true",
		ingressLabelVM:                 vmName,
		"app.kubernetes.io/managed-by": "kubego",
	}

	// Service: selector is the virt-launcher pod's stable labels. Same
	// label scheme the events/logs code already relies on.
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      resourceName,
			Namespace: c.namespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{
				"kubevirt.io":         "virt-launcher",
				"vm.kubevirt.io/name": vmName,
			},
			Ports: []corev1.ServicePort{{
				Name:       "http",
				Port:       int32(port),
				TargetPort: intstr.FromInt(port),
				Protocol:   corev1.ProtocolTCP,
			}},
		},
	}
	if _, err := c.kube.CoreV1().Services(c.namespace).Create(ctx, svc, metav1.CreateOptions{}); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return IngressInfo{}, fmt.Errorf("create service: %w", err)
		}
	}

	className := ingressClassName
	pathType := networkingv1.PathTypePrefix
	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      resourceName,
			Namespace: c.namespace,
			Labels:    labels,
		},
		Spec: networkingv1.IngressSpec{
			IngressClassName: &className,
			Rules: []networkingv1.IngressRule{{
				Host: hostname,
				IngressRuleValue: networkingv1.IngressRuleValue{
					HTTP: &networkingv1.HTTPIngressRuleValue{
						Paths: []networkingv1.HTTPIngressPath{{
							Path:     "/",
							PathType: &pathType,
							Backend: networkingv1.IngressBackend{
								Service: &networkingv1.IngressServiceBackend{
									Name: resourceName,
									Port: networkingv1.ServiceBackendPort{Number: int32(port)},
								},
							},
						}},
					},
				},
			}},
		},
	}
	if _, err := c.kube.NetworkingV1().Ingresses(c.namespace).Create(ctx, ing, metav1.CreateOptions{}); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			// Roll back the Service so the caller can retry without
			// needing to reach into kubectl.
			_ = c.kube.CoreV1().Services(c.namespace).Delete(ctx, resourceName, metav1.DeleteOptions{})
			return IngressInfo{}, fmt.Errorf("create ingress: %w", err)
		}
	}

	return IngressInfo{
		ID:         resourceName,
		VM:         vmName,
		RemotePort: port,
		Hostname:   hostname,
		URL:        ingressURL(hostname, httpPort),
		ClassName:  className,
	}, nil
}

// DeleteVMIngress removes the Ingress and Service pair for an exposure.
// Both are deleted best-effort — NotFound errors are swallowed so a
// partially-created exposure can always be cleaned up.
func (c *kubevirtClient) DeleteVMIngress(vmName, id string) error {
	if err := ValidateVMName(vmName); err != nil {
		return err
	}
	if id == "" {
		return fmt.Errorf("ingress id is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := c.kube.NetworkingV1().Ingresses(c.namespace).Delete(ctx, id, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete ingress: %w", err)
	}
	if err := c.kube.CoreV1().Services(c.namespace).Delete(ctx, id, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete service: %w", err)
	}
	return nil
}
