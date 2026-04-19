package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/rootisgod/kubego-webui/internal/api"
	"github.com/rootisgod/kubego-webui/internal/config"
	"github.com/rootisgod/kubego-webui/pkg/kubevirt"
)

var (
	Version   = "dev"
	BuildTime = "unknown"
	GitCommit = "unknown"
)

//go:embed all:frontend/dist
var frontendFS embed.FS

//go:embed cloud-init/*.yml
var builtinTemplatesFS embed.FS

//go:embed playbooks/*.yml
var builtinPlaybooksFS embed.FS

func main() {
	var (
		port       int
		configPath string
		showVer    bool
		username   string
		password   string
		kubeconfig string
		namespace  string
	)

	flag.IntVar(&port, "port", 0, "Listen port (overrides config)")
	flag.StringVar(&configPath, "config", config.DefaultConfigPath(), "Config file path")
	flag.BoolVar(&showVer, "version", false, "Print version and exit")
	flag.StringVar(&username, "username", "", "Login username (overrides config)")
	flag.StringVar(&password, "password", "", "Login password (overrides config)")
	flag.StringVar(&kubeconfig, "kubeconfig", "", "Path to kubeconfig (default: in-cluster, then $KUBECONFIG, then ~/.kube/config)")
	flag.StringVar(&namespace, "namespace", "", "Default namespace for VM operations (default: SA namespace in-cluster, else kubeconfig context)")
	flag.Parse()

	if showVer {
		fmt.Printf("KubeGo %s (built %s, commit %s)\n", Version, BuildTime, GitCommit)
		os.Exit(0)
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := config.Load(configPath)
	if err != nil {
		if !os.IsNotExist(err) {
			logger.Error("failed to load config", "path", configPath, "err", err)
			os.Exit(1)
		}
		logger.Info("no config found, creating default", "path", configPath)
		cfg, err = config.CreateDefault(configPath)
		if err != nil {
			logger.Error("failed to create config", "err", err)
			os.Exit(1)
		}
		fmt.Printf("Config: %s\n", configPath)
	}

	if migrated, err := config.MigratePassword(cfg, configPath); err != nil {
		logger.Warn("failed to migrate password to bcrypt", "err", err)
	} else if migrated {
		logger.Info("migrated plaintext password to bcrypt hash")
	}

	if port > 0 {
		cfg.Listen = fmt.Sprintf(":%d", port)
	}
	if username != "" {
		cfg.Username = username
	}
	if password != "" {
		hashed, err := config.HashPassword(password)
		if err != nil {
			logger.Error("failed to hash password", "err", err)
			os.Exit(1)
		}
		cfg.Password = hashed
	}

	// Build the cluster registry. Failure here means we cannot reach any
	// Kubernetes API at all (no in-cluster config, no kubeconfig) — that's
	// a hard error because KubeGo is useless without a cluster. Additional
	// contexts discovered in the kubeconfig are built lazily on selection.
	clusters, err := kubevirt.NewRegistry(logger, kubeconfig, namespace)
	if err != nil {
		logger.Error("failed to initialise cluster registry", "err", err)
		os.Exit(1)
	}

	// Startup CRD probe — a missing KubeVirt install is recoverable (the UI
	// shows the empty state instead of crash-looping) so we only log here.
	probeCtx, probeCancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := clusters.Active().ProbeKubeVirt(probeCtx); err != nil {
		logger.Warn("kubevirt probe failed", "err", err)
	}
	probeCancel()

	var staticFS http.Handler
	distFS, err := fs.Sub(frontendFS, "frontend/dist")
	if err != nil {
		logger.Warn("no embedded frontend found, API-only mode", "err", err)
	} else {
		staticFS = spaHandler(http.FileServerFS(distFS), distFS)
	}

	srv := api.NewServer(clusters, cfg, configPath, logger, Version, BuildTime, GitCommit, builtinTemplatesFS, builtinPlaybooksFS)
	handler := srv.Handler(staticFS)

	listen := cfg.Listen
	if !strings.Contains(listen, ":") {
		listen = ":" + listen
	}

	fmt.Printf("KubeGo %s\n", Version)
	fmt.Printf("Config: %s\n", configPath)
	fmt.Printf("Namespace: %s\n", clusters.Active().Namespace())
	fmt.Printf("Active context: %s\n", clusters.ActiveContext())
	fmt.Printf("Listening on http://0.0.0.0%s\n", listen)

	// Long-lived writes (WebSockets, SSE) preclude a blanket WriteTimeout;
	// per-request timeouts handle the bounded paths.
	httpSrv := &http.Server{
		Addr:              listen,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		logger.Info("shutting down...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := httpSrv.Shutdown(shutdownCtx); err != nil {
			logger.Warn("http shutdown error", "err", err)
		}
		srv.Shutdown()
	}()

	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("server failed", "err", err)
		os.Exit(1)
	}
}

// spaHandler serves the SPA — returns index.html for any path that doesn't match a real file.
func spaHandler(fileServer http.Handler, fsys fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/" {
			fileServer.ServeHTTP(w, r)
			return
		}
		cleanPath := strings.TrimPrefix(path, "/")
		if _, err := fs.Stat(fsys, cleanPath); err == nil {
			fileServer.ServeHTTP(w, r)
			return
		}
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})
}
