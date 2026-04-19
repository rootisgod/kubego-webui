package api

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"os/exec"
	"strings"
	"time"
)

// vmPortForward represents a single `kubectl port-forward` process that
// tunnels the VM's SSH port (:22 on the virt-launcher pod under
// KubeVirt's masquerade networking) to a random port on localhost, so
// ansible-playbook can reach the VM without the host being routable
// into the cluster's pod CIDR.
type vmPortForward struct {
	vm        string
	localPort int
	cmd       *exec.Cmd
}

// startVMPortForwards resolves each VM name to its virt-launcher pod
// and launches `kubectl port-forward pod/<pod> <random>:22` in the
// background. Returns a map[vm]localPort and a cleanup func that SIGKILLs
// every spawned kubectl. If anything fails mid-way, already-started
// forwards are torn down before returning.
//
// The port-forward pattern works because KubeVirt's default masquerade
// interface maps pod:22 -> VM:22 via iptables NAT. We don't need the
// VM's pod-CIDR IP at all — kubectl already speaks to the API server,
// and the API server proxies to the pod.
func (s *Server) startVMPortForwards(ctx context.Context, vmNames []string) (map[string]int, func(), error) {
	if len(vmNames) == 0 {
		return map[string]int{}, func() {}, nil
	}
	if _, err := exec.LookPath("kubectl"); err != nil {
		return nil, nil, fmt.Errorf("kubectl not found on PATH (required to reach VM pod CIDR): %w", err)
	}

	namespace := s.kv().Namespace()
	kubeContext := s.clusters.ActiveContext()
	kubeconfig := s.clusters.KubeconfigPath()

	ports := make(map[string]int, len(vmNames))
	forwards := make([]*vmPortForward, 0, len(vmNames))

	cleanup := func() {
		for _, f := range forwards {
			if f.cmd != nil && f.cmd.Process != nil {
				_ = f.cmd.Process.Kill()
				_ = f.cmd.Wait()
			}
		}
	}

	for _, vm := range vmNames {
		pod, err := s.kv().FindLauncherPodName(ctx, vm)
		if err != nil {
			s.logger.Warn("port-forward: skip VM (lookup failed)", "vm", vm, "err", err)
			continue
		}
		if pod == "" {
			s.logger.Warn("port-forward: skip VM (no running virt-launcher pod)", "vm", vm)
			continue
		}
		port, err := pickFreePort()
		if err != nil {
			cleanup()
			return nil, nil, fmt.Errorf("pick free local port: %w", err)
		}
		pf, err := s.startSinglePortForward(kubeContext, kubeconfig, namespace, pod, port, vm)
		if err != nil {
			cleanup()
			return nil, nil, fmt.Errorf("start port-forward for %q: %w", vm, err)
		}
		forwards = append(forwards, pf)
		ports[vm] = port
	}
	if len(ports) == 0 && len(vmNames) > 0 {
		return nil, nil, fmt.Errorf("no target VMs had a running virt-launcher pod")
	}
	return ports, cleanup, nil
}

// startSinglePortForward spawns one `kubectl port-forward` and blocks
// until either the child prints its ready line or the 5-second probe
// times out. The child continues running in the background; callers
// kill it via the cleanup func returned by startVMPortForwards.
func (s *Server) startSinglePortForward(kubeContext, kubeconfig, namespace, pod string, localPort int, vm string) (*vmPortForward, error) {
	args := []string{}
	if kubeContext != "" && kubeContext != "in-cluster" {
		args = append(args, "--context", kubeContext)
	}
	args = append(args, "-n", namespace, "port-forward", "pod/"+pod, fmt.Sprintf("%d:22", localPort))

	cmd := exec.Command("kubectl", args...)
	if kubeconfig != "" {
		cmd.Env = append(cmd.Environ(), "KUBECONFIG="+kubeconfig)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("spawn kubectl: %w", err)
	}

	// Wait up to 5 seconds for kubectl's "Forwarding from 127.0.0.1:PORT ->"
	// line. The message is printed on stdout; errors surface on stderr.
	ready := make(chan struct{}, 1)
	failure := make(chan string, 2)
	go scanUntilReady(stdout, "Forwarding from", ready)
	go scanErrLines(stderr, failure)

	select {
	case <-ready:
		// Drain further output in the background so the pipe doesn't
		// fill up and deadlock the child.
		go io.Copy(io.Discard, stdout)
		go io.Copy(io.Discard, stderr)
		return &vmPortForward{vm: vm, localPort: localPort, cmd: cmd}, nil
	case msg := <-failure:
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, fmt.Errorf("kubectl port-forward reported: %s", msg)
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, fmt.Errorf("kubectl port-forward did not become ready within 5s")
	}
}

func scanUntilReady(r io.Reader, marker string, ready chan<- struct{}) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		if strings.Contains(scanner.Text(), marker) {
			select {
			case ready <- struct{}{}:
			default:
			}
			return
		}
	}
}

func scanErrLines(r io.Reader, failure chan<- string) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		select {
		case failure <- line:
		default:
		}
	}
}

// pickFreePort asks the kernel for an ephemeral TCP port and closes the
// probe socket immediately. There's a small race between close and the
// next bind, but in practice kubectl port-forward grabs the port fast
// enough and any collision surfaces as a clear "bind: address in use"
// error on stderr rather than a silent miswire.
func pickFreePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

