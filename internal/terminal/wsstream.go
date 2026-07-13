package terminal

import (
	"encoding/json"

	"github.com/gorilla/websocket"
	"k8s.io/client-go/tools/remotecommand"
)

// wsStream adapts a gorilla WebSocket connection to the interfaces
// remotecommand needs: io.ReadWriter for the terminal bytes and
// TerminalSizeQueue for TTY resizes.
//
// Wire protocol (kept backward compatible with clients that only send raw
// bytes): binary messages carry terminal bytes in both directions; text
// messages are JSON control frames from the browser, currently only
// {"type":"resize","cols":N,"rows":N} emitted by xterm.js's fit addon.
type wsStream struct {
	ws     *websocket.Conn
	buf    []byte // leftover bytes from a message larger than the Read buffer
	sizes  chan remotecommand.TerminalSize
	closed chan struct{}
}

func newWSStream(ws *websocket.Conn) *wsStream {
	return &wsStream{
		ws:     ws,
		sizes:  make(chan remotecommand.TerminalSize, 4),
		closed: make(chan struct{}),
	}
}

type controlMsg struct {
	Type string `json:"type"`
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

// Read returns bytes the browser typed. It drains any buffered leftover
// first, then blocks for the next WebSocket message. Text frames are control
// messages, not input: they are dispatched (resize) and reading continues.
func (s *wsStream) Read(p []byte) (int, error) {
	if len(s.buf) > 0 {
		n := copy(p, s.buf)
		s.buf = s.buf[n:]
		return n, nil
	}
	for {
		mt, data, err := s.ws.ReadMessage()
		if err != nil {
			close(s.closed)
			return 0, err
		}
		switch mt {
		case websocket.TextMessage:
			var m controlMsg
			if json.Unmarshal(data, &m) == nil && m.Type == "resize" && m.Cols > 0 && m.Rows > 0 {
				select { // drop the resize if remotecommand isn't draining yet
				case s.sizes <- remotecommand.TerminalSize{Width: m.Cols, Height: m.Rows}:
				default:
				}
			}
			continue
		case websocket.BinaryMessage:
			n := copy(p, data)
			if n < len(data) {
				s.buf = data[n:]
			}
			return n, nil
		default:
			continue // ignore ping/pong/close control frames
		}
	}
}

// Write sends program output to the browser as one binary frame.
func (s *wsStream) Write(p []byte) (int, error) {
	if err := s.ws.WriteMessage(websocket.BinaryMessage, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Next implements remotecommand.TerminalSizeQueue: it blocks for the next
// browser resize and returns nil once the socket is gone (which stops the
// executor's resize loop).
func (s *wsStream) Next() *remotecommand.TerminalSize {
	select {
	case sz := <-s.sizes:
		return &sz
	case <-s.closed:
		return nil
	}
}
