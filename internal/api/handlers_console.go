package api

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// shellSession tracks one client's console connection. It's mostly
// bookkeeping so GET /shell/sessions can enumerate active tabs and
// DELETE can tear them down. No ring buffer — virt-api's serial console
// is a live stream and scrollback would need per-session buffering we're
// not paying for yet.
type shellSession struct {
	ID     string
	VM     string
	alive  bool
	cancel context.CancelFunc
}

type shellSessionStore struct {
	mu       sync.Mutex
	sessions map[string]*shellSession
}

func newShellSessionStore() *shellSessionStore {
	return &shellSessionStore{sessions: make(map[string]*shellSession)}
}

func (s *shellSessionStore) create(vm string) string {
	id := uuid.NewString()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[id] = &shellSession{ID: id, VM: vm}
	return id
}

func (s *shellSessionStore) listFor(vm string) []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []map[string]any{}
	for _, sess := range s.sessions {
		if sess.VM == vm {
			out = append(out, map[string]any{
				"sessionId": sess.ID,
				"alive":     sess.alive,
			})
		}
	}
	return out
}

// bind marks the session active and records the cancel fn so DELETE can
// tear down an in-flight WS proxy. Returns false if the id is unknown
// (caller rejects the WS upgrade).
func (s *shellSessionStore) bind(id, vm string, cancel context.CancelFunc) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[id]
	if !ok || sess.VM != vm {
		return false
	}
	sess.alive = true
	sess.cancel = cancel
	return true
}

func (s *shellSessionStore) unbind(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sess, ok := s.sessions[id]; ok {
		sess.alive = false
		sess.cancel = nil
	}
}

func (s *shellSessionStore) close(id string) {
	s.mu.Lock()
	sess := s.sessions[id]
	delete(s.sessions, id)
	s.mu.Unlock()
	if sess != nil && sess.cancel != nil {
		sess.cancel()
	}
}

// wsUpgrader is permissive on Origin — the WS endpoint sits behind the
// same auth middleware as the rest of /api/v1, and CORS headers are
// applied before this handler runs. A stricter Origin check would only
// break local dev proxies.
var wsUpgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

func (s *Server) handleCreateShellSession(w http.ResponseWriter, r *http.Request) {
	name, ok := validVMName(w, r, "name")
	if !ok {
		return
	}
	id := s.shells.create(name)
	writeJSON(w, http.StatusCreated, map[string]string{"sessionId": id})
}

func (s *Server) handleListShellSessions(w http.ResponseWriter, r *http.Request) {
	name, ok := validVMName(w, r, "name")
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.shells.listFor(name))
}

func (s *Server) handleDeleteShellSession(w http.ResponseWriter, r *http.Request) {
	_, ok := validVMName(w, r, "name")
	if !ok {
		return
	}
	id := r.PathValue("sessionId")
	s.shells.close(id)
	writeMessage(w, "session closed")
}

// handleShellWS upgrades to WebSocket and pipes bytes between the
// browser's xterm.js and virt-api's serial-console subresource.
//
// Protocol the frontend uses (see frontend/src/composables/useWebSocket.js):
//   - text/binary frames are raw key strokes → pumped into the console
//   - a 5-byte binary frame {0x01, colsHi, colsLo, rowsHi, rowsLo}
//     encodes a terminal resize. Dropped here: the serial console is
//     fixed-size and doesn't honor SIGWINCH.
//
// On disconnect (either side), both goroutines bail and defers clean up.
func (s *Server) handleShellWS(w http.ResponseWriter, r *http.Request) {
	name, ok := validVMName(w, r, "name")
	if !ok {
		return
	}
	sessionID := r.PathValue("sessionId")

	clientWS, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		s.logger.Error("shell ws upgrade failed", "vm", name, "err", err)
		return
	}
	defer clientWS.Close()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	if !s.shells.bind(sessionID, name, cancel) {
		closeWS(clientWS, websocket.ClosePolicyViolation, "unknown session id")
		return
	}
	defer s.shells.unbind(sessionID)

	stream, err := s.kv().Console(ctx, name)
	if err != nil {
		s.logger.Error("open console failed", "vm", name, "err", err)
		closeWS(clientWS, websocket.CloseInternalServerErr, err.Error())
		return
	}
	defer stream.Close()

	// stream → client
	done := make(chan struct{}, 2)
	go func() {
		defer func() { done <- struct{}{} }()
		buf := make([]byte, 4096)
		for {
			n, err := stream.Read(buf)
			if err != nil {
				return
			}
			if err := clientWS.WriteMessage(websocket.BinaryMessage, buf[:n]); err != nil {
				return
			}
		}
	}()

	// client → stream
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
			// Drop terminal-resize frames — see comment above.
			if len(data) == 5 && data[0] == 0x01 {
				continue
			}
			if _, err := stream.Write(data); err != nil {
				return
			}
		}
	}()

	<-done
	cancel()
}

func closeWS(c *websocket.Conn, code int, msg string) {
	_ = c.WriteControl(websocket.CloseMessage,
		websocket.FormatCloseMessage(code, truncateErr(msg)),
		time.Now().Add(time.Second))
}

// truncateErr keeps WebSocket close reasons within the 123-byte limit
// the RFC imposes. virt-api errors can be arbitrarily long.
func truncateErr(s string) string {
	const max = 123
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

