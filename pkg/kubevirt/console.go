package kubevirt

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"k8s.io/client-go/rest"
)

// Console dials the VMI's serial-console subresource on virt-api and
// returns a raw byte stream. Keystrokes write to the guest tty; reads
// yield console output. KubeVirt's console endpoint doesn't honor
// SIGWINCH, so callers that receive terminal-resize frames should drop
// them rather than forward them.
//
// The stream closes when the VMI dies, the VM stops, or the caller
// Close()s it. Authorisation reuses the driver's rest.Config — bearer
// token when present, client cert otherwise.
func (c *kubevirtClient) Console(ctx context.Context, vmName string) (io.ReadWriteCloser, error) {
	if err := ValidateVMName(vmName); err != nil {
		return nil, err
	}
	u, err := consoleWSURL(c.restCfg.Host, c.namespace, vmName)
	if err != nil {
		return nil, fmt.Errorf("build console URL: %w", err)
	}

	tlsCfg, err := rest.TLSConfigFor(c.restCfg)
	if err != nil {
		return nil, fmt.Errorf("build tls config: %w", err)
	}
	if tlsCfg == nil {
		// happens for unencrypted loopback apiservers — synthesise a
		// permissive config rather than nil so the dialer still works
		tlsCfg = &tls.Config{}
	}

	token, err := bearerTokenForConfig(c.restCfg.BearerToken, c.restCfg.BearerTokenFile)
	if err != nil {
		return nil, err
	}

	dialer := &websocket.Dialer{
		TLSClientConfig:  tlsCfg,
		HandshakeTimeout: 15 * time.Second,
		// KubeVirt accepts the plain subprotocol; omitting it works
		// against recent virt-api but we name it explicitly so
		// future-us can tell at a glance what's on the wire.
		Subprotocols: []string{"plain.kubevirt.io"},
	}

	headers := http.Header{}
	if token != "" {
		headers.Set("Authorization", "Bearer "+token)
	}

	conn, resp, err := dialer.DialContext(ctx, u.String(), headers)
	if err != nil {
		status := ""
		if resp != nil {
			status = fmt.Sprintf(" (http %s)", resp.Status)
		}
		return nil, fmt.Errorf("dial console %s: %w%s", u.String(), err, status)
	}
	return &consoleStream{ws: conn}, nil
}

func consoleWSURL(apiHost, namespace, vmName string) (*url.URL, error) {
	u, err := url.Parse(apiHost)
	if err != nil {
		return nil, err
	}
	switch strings.ToLower(u.Scheme) {
	case "https", "wss":
		u.Scheme = "wss"
	case "http", "ws":
		u.Scheme = "ws"
	default:
		return nil, fmt.Errorf("unsupported apiserver scheme %q", u.Scheme)
	}
	u.Path = fmt.Sprintf("/apis/subresources.kubevirt.io/v1/namespaces/%s/virtualmachineinstances/%s/console", namespace, vmName)
	u.RawQuery = ""
	return u, nil
}

// consoleStream adapts gorilla's websocket.Conn to an io.ReadWriteCloser.
// Reads return the next frame's bytes, buffering leftovers across calls.
// Writes send one BinaryMessage per call — the virt-api console treats
// inbound frames as a byte stream and does not require message framing.
type consoleStream struct {
	ws  *websocket.Conn
	buf []byte
}

func (s *consoleStream) Read(p []byte) (int, error) {
	for len(s.buf) == 0 {
		_, data, err := s.ws.ReadMessage()
		if err != nil {
			return 0, err
		}
		s.buf = data
	}
	n := copy(p, s.buf)
	s.buf = s.buf[n:]
	return n, nil
}

func (s *consoleStream) Write(p []byte) (int, error) {
	if err := s.ws.WriteMessage(websocket.BinaryMessage, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (s *consoleStream) Close() error {
	_ = s.ws.WriteControl(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
		time.Now().Add(time.Second))
	return s.ws.Close()
}

// bearerTokenForConfig returns the bearer token from a rest.Config,
// reading BearerTokenFile lazily. Empty string means cert-based auth.
func bearerTokenForConfig(cfgBearerToken, cfgBearerTokenFile string) (string, error) {
	if cfgBearerToken != "" {
		return cfgBearerToken, nil
	}
	if cfgBearerTokenFile != "" {
		data, err := os.ReadFile(cfgBearerTokenFile)
		if err != nil {
			return "", fmt.Errorf("read bearer token: %w", err)
		}
		return strings.TrimSpace(string(data)), nil
	}
	return "", nil
}
