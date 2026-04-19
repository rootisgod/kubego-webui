package api

import (
	"context"
	"net/http"

	"github.com/gorilla/websocket"
)

// vncWSUpgrader differs from the shell upgrader in one place: it offers
// the `binary` subprotocol that noVNC asks for by default. gorilla only
// echoes a subprotocol back if it's listed here; noVNC on some versions
// treats a missing subprotocol as a fatal error.
var vncWSUpgrader = websocket.Upgrader{
	ReadBufferSize:  16 * 1024,
	WriteBufferSize: 16 * 1024,
	CheckOrigin:     func(r *http.Request) bool { return true },
	Subprotocols:    []string{"binary"},
}

// handleVMVNC upgrades the incoming connection to WebSocket and bridges
// RFB bytes between noVNC in the browser and virt-api's vnc subresource.
// Unlike the serial console there is no session store — VNC is a live
// framebuffer stream, refreshing the tab just reconnects.
func (s *Server) handleVMVNC(w http.ResponseWriter, r *http.Request) {
	name, ok := validVMName(w, r, "name")
	if !ok {
		return
	}

	clientWS, err := vncWSUpgrader.Upgrade(w, r, nil)
	if err != nil {
		s.logger.Error("vnc ws upgrade failed", "vm", name, "err", err)
		return
	}
	defer clientWS.Close()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	stream, err := s.kv().VNC(ctx, name)
	if err != nil {
		s.logger.Error("open vnc failed", "vm", name, "err", err)
		closeWS(clientWS, websocket.CloseInternalServerErr, err.Error())
		return
	}
	defer stream.Close()

	done := make(chan struct{}, 2)

	// virt-api → client
	go func() {
		defer func() { done <- struct{}{} }()
		buf := make([]byte, 16*1024)
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

	// client → virt-api
	go func() {
		defer func() { done <- struct{}{} }()
		for {
			mt, data, err := clientWS.ReadMessage()
			if err != nil {
				return
			}
			if mt != websocket.BinaryMessage && mt != websocket.TextMessage {
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
