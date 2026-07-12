package terminal

import (
	"github.com/gorilla/websocket"
)

// wsStream adapts a gorilla WebSocket connection to io.ReadWriter so
// remotecommand can treat it as the terminal's stdin/stdout. Binary
// messages carry raw terminal bytes in both directions.
type wsStream struct {
	ws  *websocket.Conn
	buf []byte // leftover bytes from a message larger than the Read buffer
}

// Read returns bytes the browser typed. It drains any buffered leftover
// first, then blocks for the next WebSocket message.
func (s *wsStream) Read(p []byte) (int, error) {
	if len(s.buf) > 0 {
		n := copy(p, s.buf)
		s.buf = s.buf[n:]
		return n, nil
	}
	for {
		mt, data, err := s.ws.ReadMessage()
		if err != nil {
			return 0, err
		}
		if mt != websocket.BinaryMessage && mt != websocket.TextMessage {
			continue // ignore ping/pong/close control frames
		}
		n := copy(p, data)
		if n < len(data) {
			s.buf = data[n:]
		}
		return n, nil
	}
}

// Write sends program output to the browser as one binary frame.
func (s *wsStream) Write(p []byte) (int, error) {
	if err := s.ws.WriteMessage(websocket.BinaryMessage, p); err != nil {
		return 0, err
	}
	return len(p), nil
}
