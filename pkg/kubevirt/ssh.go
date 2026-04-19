package kubevirt

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// vmSSH wraps *ssh.Client so callers can Close() a single thing and get
// both the SSH connection and the underlying port-forward torn down.
type vmSSH struct {
	*ssh.Client
	fwdStop func()
}

func (s *vmSSH) Close() error {
	err := s.Client.Close()
	if s.fwdStop != nil {
		s.fwdStop()
	}
	return err
}

// openSSHToVM establishes an SSH connection to the given VM, reusing the
// server's managed key. The SSH user is picked from the VM's image label
// (ubuntu/debian/fedora/…), falling back to "ubuntu" when the label is
// absent or unrecognised. Caller Close()s the returned session to tear
// down both the SSH connection and the backing port-forward.
func (c *kubevirtClient) openSSHToVM(ctx context.Context, vmName string) (*vmSSH, error) {
	if c.sshPrivateKeyPath == "" {
		return nil, fmt.Errorf("ssh private key path not configured — set it in the KubeGo config")
	}
	keyBytes, err := os.ReadFile(c.sshPrivateKeyPath)
	if err != nil {
		return nil, fmt.Errorf("read ssh key %q: %w", c.sshPrivateKeyPath, err)
	}
	signer, err := ssh.ParsePrivateKey(keyBytes)
	if err != nil {
		return nil, fmt.Errorf("parse ssh key: %w", err)
	}

	user, err := c.sshUserForVM(ctx, vmName)
	if err != nil {
		return nil, err
	}

	conn, stop, err := c.DialVMPort(ctx, vmName, 22)
	if err != nil {
		return nil, err
	}

	cfg := &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         15 * time.Second,
	}

	// Bound the SSH handshake so a half-open VM (cloud-init not done) can't
	// stall the caller forever. The underlying conn already has Keepalives.
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	}
	sshConn, chans, reqs, err := ssh.NewClientConn(conn, net.JoinHostPort(vmName, "22"), cfg)
	if err != nil {
		_ = conn.Close()
		stop()
		return nil, fmt.Errorf("ssh handshake with VM %q: %w", vmName, err)
	}
	// Remove the handshake deadline — long-running sessions (exec streaming,
	// tar pipes) set their own via context.
	_ = conn.SetDeadline(time.Time{})

	client := ssh.NewClient(sshConn, chans, reqs)
	return &vmSSH{Client: client, fwdStop: stop}, nil
}

// sshUserForVM resolves the cloud-image default user for a VM via its
// kubego.io/release label. Falls back to "ubuntu" — safe for the legacy
// Ubuntu-only catalog and any image we don't know about.
func (c *kubevirtClient) sshUserForVM(ctx context.Context, vmName string) (string, error) {
	vm, err := c.dyn.Resource(vmGVR).Namespace(c.namespace).Get(ctx, vmName, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("lookup VM %q: %w", vmName, err)
	}
	release, _, _ := unstructured.NestedString(vm.Object, "metadata", "labels", "kubego.io/release")
	return sshUserForImage(release), nil
}

// sshUserForImage returns the cloud-image default user baked into each
// containerdisk in our catalog. Unknown inputs default to ubuntu because
// the legacy launch path passes bare Ubuntu release tags ("22.04") and
// all Ubuntu images ship `ubuntu` as the default user.
func sshUserForImage(name string) string {
	switch name {
	case "debian-12":
		return "debian"
	case "fedora-40":
		return "fedora"
	case "centos-stream-9":
		return "centos"
	case "rockylinux-9":
		return "cloud-user"
	}
	return "ubuntu"
}

// ExecInVM runs a command on the VM over SSH and returns its stdout.
// Stderr is surfaced in the error on non-zero exit. Uses the server's
// managed key — VMs launched before that key existed will fail with an
// authentication error, which is the right signal to recreate them.
func (c *kubevirtClient) ExecInVM(vmName string, command []string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return c.ExecInVMWithContext(ctx, vmName, command)
}

func (c *kubevirtClient) ExecInVMWithContext(ctx context.Context, vmName string, command []string) (string, error) {
	return c.ExecInVMStreaming(ctx, vmName, command, nil)
}

func (c *kubevirtClient) ExecInVMStreaming(ctx context.Context, vmName string, command []string, onLine func(string)) (string, error) {
	if len(command) == 0 {
		return "", fmt.Errorf("command is required")
	}
	client, err := c.openSSHToVM(ctx, vmName)
	if err != nil {
		return "", err
	}
	defer client.Close()
	sess, err := client.NewSession()
	if err != nil {
		return "", fmt.Errorf("new ssh session: %w", err)
	}
	defer sess.Close()

	stdout, err := sess.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("stdout pipe: %w", err)
	}
	var stderrBuf bytes.Buffer
	sess.Stderr = &stderrBuf

	cmd := joinShell(command)
	if err := sess.Start(cmd); err != nil {
		return "", fmt.Errorf("start ssh exec: %w", err)
	}

	// Cancellation: signal the session on ctx.Done so a hung guest command
	// releases the caller instead of holding the connection open.
	doneCh := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = sess.Signal(ssh.SIGKILL)
		case <-doneCh:
		}
	}()

	var out bytes.Buffer
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if onLine != nil {
			onLine(line)
		}
		out.WriteString(line)
		out.WriteByte('\n')
	}
	waitErr := sess.Wait()
	close(doneCh)

	if ctx.Err() != nil {
		return out.String(), ctx.Err()
	}
	if waitErr != nil {
		stderr := strings.TrimSpace(stderrBuf.String())
		if stderr != "" {
			return out.String(), fmt.Errorf("%w: %s", waitErr, stderr)
		}
		return out.String(), waitErr
	}
	return out.String(), nil
}

// TransferFromVM streams a file out of the VM by running `cat $path` over
// SSH. Simpler than SCP/SFTP, works on any distro with coreutils, and
// avoids pulling in an extra dependency for a one-line use case.
func (c *kubevirtClient) TransferFromVM(vmName, remotePath string, w io.Writer) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	client, err := c.openSSHToVM(ctx, vmName)
	if err != nil {
		return err
	}
	defer client.Close()
	sess, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("new ssh session: %w", err)
	}
	defer sess.Close()

	sess.Stdout = w
	var stderrBuf bytes.Buffer
	sess.Stderr = &stderrBuf

	if err := sess.Run("cat " + shellQuote(remotePath)); err != nil {
		stderr := strings.TrimSpace(stderrBuf.String())
		if stderr != "" {
			return fmt.Errorf("download %q: %w: %s", remotePath, err, stderr)
		}
		return fmt.Errorf("download %q: %w", remotePath, err)
	}
	return nil
}

// TransferToVM writes r into the VM at `remotePath` by piping through
// `sh -c 'cat > "$1"' _ <path>`. The positional-arg form avoids embedding
// the caller-supplied path inside shell quotes twice, which would expose
// a shell-injection vector if `remotePath` ever contained quoting chars.
func (c *kubevirtClient) TransferToVM(vmName, remotePath string, r io.Reader) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	client, err := c.openSSHToVM(ctx, vmName)
	if err != nil {
		return err
	}
	defer client.Close()
	sess, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("new ssh session: %w", err)
	}
	defer sess.Close()

	sess.Stdin = r
	var stderrBuf bytes.Buffer
	sess.Stderr = &stderrBuf

	// Quote remotePath once; the outer sh consumes one level of quoting.
	cmd := fmt.Sprintf("sh -c 'cat > \"$1\"' _ %s", shellQuote(remotePath))
	if err := sess.Run(cmd); err != nil {
		stderr := strings.TrimSpace(stderrBuf.String())
		if stderr != "" {
			return fmt.Errorf("upload %q: %w: %s", remotePath, err, stderr)
		}
		return fmt.Errorf("upload %q: %w", remotePath, err)
	}
	return nil
}

// joinShell single-quotes every argument and joins with spaces, producing
// a command string the remote login shell can parse without surprises.
// We intentionally do not rely on the remote shell honouring a particular
// quoting style — POSIX sh single-quote + escaped-single-quote is the
// universal safe form.
func joinShell(args []string) string {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = shellQuote(a)
	}
	return strings.Join(parts, " ")
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
