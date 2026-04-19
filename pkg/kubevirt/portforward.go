package kubevirt

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

// StartPortForward opens a kubectl-style port-forward from an ephemeral
// local port (127.0.0.1) to `remotePort` on the VM's virt-launcher pod,
// and returns the local port + a stop function. The forward stays up
// until stop is called. Works in both in-cluster and external-kubeconfig
// modes — the stream travels over the apiserver proxy so the host does
// not need to be routable into the pod CIDR.
//
// Callers that just want a single TCP connection should use DialVMPort,
// which wraps this and immediately dials the returned local port.
func (c *kubevirtClient) StartPortForward(ctx context.Context, vmName string, remotePort int) (int, func(), error) {
	if err := ValidateVMName(vmName); err != nil {
		return 0, nil, err
	}
	if remotePort <= 0 {
		return 0, nil, fmt.Errorf("invalid remote port %d", remotePort)
	}

	podName, err := c.FindLauncherPodName(ctx, vmName)
	if err != nil {
		return 0, nil, fmt.Errorf("find launcher pod for VM %q: %w", vmName, err)
	}
	if podName == "" {
		return 0, nil, fmt.Errorf("no running virt-launcher pod for VM %q", vmName)
	}

	transport, upgrader, err := spdy.RoundTripperFor(c.restCfg)
	if err != nil {
		return 0, nil, fmt.Errorf("build spdy transport: %w", err)
	}

	req := c.kube.CoreV1().RESTClient().Post().
		Resource("pods").
		Namespace(c.namespace).
		Name(podName).
		SubResource("portforward")

	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, "POST", req.URL())

	stopCh := make(chan struct{})
	readyCh := make(chan struct{})
	errCh := make(chan error, 1)

	fw, err := portforward.New(dialer, []string{fmt.Sprintf("0:%d", remotePort)}, stopCh, readyCh, io.Discard, io.Discard)
	if err != nil {
		return 0, nil, fmt.Errorf("new port-forward: %w", err)
	}

	go func() {
		errCh <- fw.ForwardPorts()
	}()

	select {
	case <-readyCh:
	case err := <-errCh:
		return 0, nil, fmt.Errorf("port-forward did not become ready: %w", err)
	case <-ctx.Done():
		close(stopCh)
		return 0, nil, ctx.Err()
	case <-time.After(10 * time.Second):
		close(stopCh)
		return 0, nil, fmt.Errorf("port-forward did not become ready within 10s")
	}

	ports, err := fw.GetPorts()
	if err != nil || len(ports) == 0 {
		close(stopCh)
		return 0, nil, fmt.Errorf("get forwarded ports: %w", err)
	}

	stop := func() {
		// Closing stopCh is idempotent-safe only once — guard with a
		// recover in case a caller stops twice.
		defer func() { _ = recover() }()
		close(stopCh)
	}
	return int(ports[0].Local), stop, nil
}

// DialVMPort opens a single TCP connection to `remotePort` on the VM,
// backed by a port-forward tunnel that is torn down when the returned
// stop function is called.
func (c *kubevirtClient) DialVMPort(ctx context.Context, vmName string, remotePort int) (net.Conn, func(), error) {
	localPort, stop, err := c.StartPortForward(ctx, vmName, remotePort)
	if err != nil {
		return nil, nil, err
	}
	conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", fmt.Sprintf("127.0.0.1:%d", localPort))
	if err != nil {
		stop()
		return nil, nil, fmt.Errorf("dial local forwarded port %d: %w", localPort, err)
	}
	return conn, stop, nil
}
