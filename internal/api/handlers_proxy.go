package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/rootisgod/kubego-webui/pkg/kubevirt"
)

// handleVMProxy proxies `/proxy/<vm>/<remotePort>/<path>` to the given
// port on the given VM, opening a port-forward via the manager on demand.
// The browser experience is "hit a URL and see the VM's web app" — no
// kubectl, no local port wrangling.
//
// Caveats shipped deliberately:
//   - Path-prefix proxying: apps that emit absolute-path URLs (e.g. a
//     redirect to `/login`) will bounce the browser off KubeGo's path
//     space instead of back through the proxy. The fix is app-side (use
//     relative URLs) or swap to Tier 1-B ingress when the app needs it.
//   - Single host target. WebSockets pass through because
//     httputil.ReverseProxy upgrades them since Go 1.12.
func (s *Server) handleVMProxy(w http.ResponseWriter, r *http.Request) {
	remainder := strings.TrimPrefix(r.URL.Path, "/proxy/")
	parts := strings.SplitN(remainder, "/", 3)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		http.Error(w, "usage: /proxy/<vm>/<port>/<path>", http.StatusBadRequest)
		return
	}
	vm := parts[0]
	if err := kubevirt.ValidateVMName(vm); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	port, err := strconv.Atoi(parts[1])
	if err != nil || port <= 0 || port > 65535 {
		http.Error(w, "invalid port", http.StatusBadRequest)
		return
	}
	rest := "/"
	if len(parts) == 3 {
		rest = "/" + parts[2]
	}

	openCtx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	entry, err := s.portForwards.EnsureOpen(openCtx, vm, port, "http", "")
	if err != nil {
		http.Error(w, "open port-forward: "+err.Error(), http.StatusBadGateway)
		return
	}

	target := &url.URL{
		Scheme: "http",
		Host:   fmt.Sprintf("127.0.0.1:%d", entry.LocalPort),
	}

	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.URL.Path = rest
			pr.Out.URL.RawPath = ""
			pr.Out.Host = target.Host
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			s.logger.Warn("proxy upstream error", "vm", vm, "port", port, "err", err)
			http.Error(w, "upstream error: "+err.Error(), http.StatusBadGateway)
		},
	}
	proxy.ServeHTTP(w, r)

	// Serving is synchronous even for WebSockets (Hijack blocks until the
	// client disconnects), so touching LastUsed here captures real usage
	// rather than just request start.
	s.portForwards.touchByKey(entry.Context, vm, port)
}
