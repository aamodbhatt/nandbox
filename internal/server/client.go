package server

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// sendQueue bounds the reliable (JSON) messages queued for one client. Edits
// must never be dropped, so a client that falls this far behind is
// disconnected and resyncs on reconnect.
const sendQueue = 256

var palette = []string{"#ff6b6b", "#ffd93d", "#6bcBff", "#c792ea", "#5eead4", "#f78c6c", "#82aaff", "#a3e635"}

// client is one WebSocket connection. Reliable messages go through a bounded
// queue; signal and dock frames go through single-slot mailboxes that keep
// only the newest frame, so a slow client gets fewer frames, never stale ones,
// and never slows the room down (back-pressure by coalescing).
type client struct {
	id, name, color string
	x, y            float64
	path            []string
	view            rect

	conn   *websocket.Conn
	send   chan []byte
	mu     sync.Mutex
	frames map[byte][]byte
	wake   chan struct{}
	closed chan struct{}
	once   sync.Once
}

func newClient(id string, n int, conn *websocket.Conn) *client {
	return &client{id: id, name: "guest-" + id[:4], color: palette[n%len(palette)], conn: conn,
		send: make(chan []byte, sendQueue), frames: map[byte][]byte{}, wake: make(chan struct{}, 1), closed: make(chan struct{})}
}

func (c *client) close() { c.once.Do(func() { close(c.closed) }) }

func (c *client) sendJSON(b []byte) {
	select {
	case c.send <- b:
	default:
		c.close() // too slow for the reliable stream
	}
}

func (c *client) sendValue(v any) {
	b, err := json.Marshal(v)
	if err == nil {
		c.sendJSON(b)
	}
}

func (c *client) sendFrame(tag byte, frame []byte) {
	c.mu.Lock()
	c.frames[tag] = frame
	c.mu.Unlock()
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

func (c *client) takeFrames() [][]byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out [][]byte
	for tag, f := range c.frames {
		out = append(out, f)
		delete(c.frames, tag)
	}
	return out
}

// writeLoop drains the reliable queue first, then the newest frames.
func (c *client) writeLoop(ctx context.Context) {
	write := func(typ websocket.MessageType, b []byte) bool {
		wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		return c.conn.Write(wctx, typ, b) == nil
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.closed:
			return
		case b := <-c.send:
			if !write(websocket.MessageText, b) {
				c.close()
				return
			}
		case <-c.wake:
			for _, f := range c.takeFrames() {
				if !write(websocket.MessageBinary, f) {
					c.close()
					return
				}
			}
		}
	}
}

// readLoop forwards client messages to the room until the socket closes.
func (c *client) readLoop(ctx context.Context, r *Room) {
	c.conn.SetReadLimit(1 << 20)
	for {
		_, data, err := c.conn.Read(ctx)
		if err != nil {
			c.close()
			return
		}
		var m clientMsg
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		select {
		case r.inbox <- inbound{c, m}:
		case <-c.closed:
			return
		}
	}
}
