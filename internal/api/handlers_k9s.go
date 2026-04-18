package api

import (
	"encoding/binary"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/creack/pty"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// k9sSession is a single live k9s subprocess attached to a PTY and
// proxied over a WebSocket. One session per tab in the UI. When the
// browser disconnects (or DELETE hits us) we kill the subprocess and
// free the PTY.
type k9sSession struct {
	ID      string
	Context string // kubeconfig context k9s was spawned against
	Started time.Time

	mu    sync.Mutex
	alive bool
	cmd   *exec.Cmd
	ptmx  *os.File
}

type k9sSessionStore struct {
	mu       sync.Mutex
	sessions map[string]*k9sSession
}

func newK9sSessionStore() *k9sSessionStore {
	return &k9sSessionStore{sessions: make(map[string]*k9sSession)}
}

func (s *k9sSessionStore) create(kctx string) *k9sSession {
	sess := &k9sSession{
		ID:      uuid.NewString(),
		Context: kctx,
		Started: time.Now(),
	}
	s.mu.Lock()
	s.sessions[sess.ID] = sess
	s.mu.Unlock()
	return sess
}

func (s *k9sSessionStore) get(id string) *k9sSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[id]
}

func (s *k9sSessionStore) list() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []map[string]any{}
	for _, sess := range s.sessions {
		sess.mu.Lock()
		out = append(out, map[string]any{
			"sessionId": sess.ID,
			"context":   sess.Context,
			"alive":     sess.alive,
			"started":   sess.Started.Format(time.RFC3339),
		})
		sess.mu.Unlock()
	}
	return out
}

func (s *k9sSessionStore) closeAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, sess := range s.sessions {
		sess.kill()
		delete(s.sessions, id)
	}
}

func (s *k9sSessionStore) close(id string) {
	s.mu.Lock()
	sess, ok := s.sessions[id]
	delete(s.sessions, id)
	s.mu.Unlock()
	if ok {
		sess.kill()
	}
}

// kill tears down the PTY + subprocess. Safe to call multiple times.
func (sess *k9sSession) kill() {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.ptmx != nil {
		_ = sess.ptmx.Close()
		sess.ptmx = nil
	}
	if sess.cmd != nil && sess.cmd.Process != nil {
		_ = sess.cmd.Process.Kill()
		sess.cmd = nil
	}
	sess.alive = false
}

// --- handlers ---

// handleCreateK9sSession reserves a session id; the actual PTY spawn
// happens lazily on WS upgrade. Keeps the REST path simple (no server-
// side state to roll back if the browser never follows through).
func (s *Server) handleCreateK9sSession(w http.ResponseWriter, r *http.Request) {
	if !k9sAvailable() {
		writeError(w, http.StatusPreconditionFailed, "k9s not installed on server PATH")
		return
	}
	if s.clusters.InCluster() {
		writeError(w, http.StatusPreconditionFailed, "k9s sessions are disabled when running in-cluster")
		return
	}
	if s.clusters.ActiveContext() == "" {
		writeError(w, http.StatusPreconditionFailed, "no active cluster — create or select a context first")
		return
	}
	sess := s.k9s.create(s.clusters.ActiveContext())
	writeJSON(w, http.StatusCreated, map[string]string{
		"sessionId": sess.ID,
		"context":   sess.Context,
	})
}

func (s *Server) handleListK9sSessions(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.k9s.list())
}

func (s *Server) handleDeleteK9sSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("sessionId")
	s.k9s.close(id)
	writeMessage(w, "session closed")
}

// handleK9sWS upgrades to WebSocket, spawns `k9s --context <ctx>` in a
// PTY, and proxies bytes both ways. Uses the same 5-byte {0x01, cols,
// rows} resize-frame protocol as the serial console, but actually
// forwards the resize to the PTY (k9s redraws on SIGWINCH).
func (s *Server) handleK9sWS(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("sessionId")
	sess := s.k9s.get(sessionID)
	if sess == nil {
		writeError(w, http.StatusNotFound, "unknown session id")
		return
	}

	clientWS, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		s.logger.Error("k9s ws upgrade failed", "err", err)
		return
	}
	defer clientWS.Close()

	cmd := exec.Command("k9s", "--context", sess.Context)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	if kcfg := s.clusters.KubeconfigPath(); kcfg != "" {
		cmd.Env = append(cmd.Env, "KUBECONFIG="+kcfg)
	}

	ptmx, err := pty.Start(cmd)
	if err != nil {
		s.logger.Error("k9s pty start failed", "err", err, "session", sessionID)
		closeWS(clientWS, websocket.CloseInternalServerErr, "pty start: "+err.Error())
		s.k9s.close(sessionID)
		return
	}

	sess.mu.Lock()
	sess.cmd = cmd
	sess.ptmx = ptmx
	sess.alive = true
	sess.mu.Unlock()

	s.logger.Info("k9s session started", "session", sessionID, "context", sess.Context, "pid", cmd.Process.Pid)

	// Initial window size — xterm.js will send another resize frame on
	// mount with the real dimensions, but 120x32 avoids "1x1 until
	// client connects" flicker.
	_ = pty.Setsize(ptmx, &pty.Winsize{Rows: 32, Cols: 120})

	defer func() {
		sess.kill()
		s.logger.Info("k9s session ended", "session", sessionID)
	}()

	done := make(chan struct{}, 2)

	// pty → client
	go func() {
		defer func() { done <- struct{}{} }()
		buf := make([]byte, 8192)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				if werr := clientWS.WriteMessage(websocket.BinaryMessage, buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	// client → pty (plus resize frames)
	go func() {
		defer func() { done <- struct{}{} }()
		for {
			mt, data, err := clientWS.ReadMessage()
			if err != nil {
				return
			}
			if mt != websocket.TextMessage && mt != websocket.BinaryMessage {
				continue
			}
			// 5-byte resize frame: {0x01, colsHi, colsLo, rowsHi, rowsLo}
			if len(data) == 5 && data[0] == 0x01 {
				cols := binary.BigEndian.Uint16(data[1:3])
				rows := binary.BigEndian.Uint16(data[3:5])
				if cols > 0 && rows > 0 {
					_ = pty.Setsize(ptmx, &pty.Winsize{Rows: rows, Cols: cols})
				}
				continue
			}
			if _, werr := ptmx.Write(data); werr != nil {
				return
			}
		}
	}()

	<-done
}

// handleK9sInstall runs scripts/k9s-install.sh and streams output over
// SSE. Re-uses the streamCommand helper from handlers_clusters.go.
func (s *Server) handleK9sInstall(w http.ResponseWriter, r *http.Request) {
	if s.clusters.InCluster() {
		writeError(w, http.StatusPreconditionFailed, "install is disabled when running in-cluster")
		return
	}
	scriptPath := "scripts/k9s-install.sh"
	if _, err := os.Stat(scriptPath); err != nil {
		writeError(w, http.StatusPreconditionFailed, fmt.Sprintf("install script not found at %s (run from the repo root)", scriptPath))
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}
	setSSEHeaders(w, flusher)

	if err := streamCommand(r.Context(), w, flusher, "bash", scriptPath); err != nil {
		writeClusterSSE(w, flusher, clusterSSEEvent{Type: "error", Error: err.Error()})
		return
	}
	writeClusterSSE(w, flusher, clusterSSEEvent{Type: "done"})
}

// --- helpers ---

func k9sAvailable() bool {
	_, err := exec.LookPath("k9s")
	return err == nil
}
