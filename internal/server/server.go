// Package server hosts multiplayer rooms over HTTP and WebSockets: a
// server-authoritative edit log with undo/redo, presence cursors, simulation
// control and per-viewport signal streaming.
package server

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"

	"github.com/coder/websocket"
)

var roomIDPattern = regexp.MustCompile(`^[a-z0-9]{6,20}$`)

// Server owns the room registry.
type Server struct {
	store   *Store
	assets  fs.FS
	mu      sync.Mutex
	rooms   map[string]*Room
	stop    map[string]chan struct{}
	clients int
	wg      sync.WaitGroup
}

// New creates a server. store may be nil (rooms then live in memory only).
func New(store *Store, assets fs.FS) *Server {
	return &Server{store: store, assets: assets, rooms: map[string]*Room{}, stop: map[string]chan struct{}{}}
}

func newID() string {
	b := make([]byte, 5)
	_, _ = rand.Read(b)
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b))
}

// Handler returns the HTTP routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/rooms", s.createRoom)
	mux.HandleFunc("GET /api/rooms/{id}/ws", s.join)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	files := http.FileServerFS(s.assets)
	mux.HandleFunc("GET /r/{id}", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFileFS(w, r, s.assets, "index.html")
	})
	mux.Handle("GET /", files)
	return mux
}

func (s *Server) createRoom(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Template string `json:"template"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&req)
	if req.Template == "" {
		req.Template = "snake"
	}
	d, err := NewTemplate(req.Template)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	id := newID()
	room := newRoom(id, req.Template, d, 0, s.store)
	room.dirty = true
	s.start(room)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"id": id})
}

func (s *Server) start(room *Room) {
	s.mu.Lock()
	defer s.mu.Unlock()
	done := make(chan struct{})
	s.rooms[room.id], s.stop[room.id] = room, done
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		room.run(done)
	}()
}

// room returns a live room, loading it from the store if needed.
func (s *Server) room(id string) (*Room, error) {
	s.mu.Lock()
	r, ok := s.rooms[id]
	s.mu.Unlock()
	if ok || s.store == nil {
		return r, nil
	}
	tmpl, d, seq, found, err := s.store.Load(id)
	if err != nil || !found {
		return nil, err
	}
	r = newRoom(id, tmpl, d, seq, s.store)
	s.start(r)
	return r, nil
}

func (s *Server) join(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !roomIDPattern.MatchString(id) {
		http.Error(w, "bad room id", http.StatusBadRequest)
		return
	}
	room, err := s.room(id)
	if err != nil {
		slog.Error("load room", "room", id, "err", err)
		http.Error(w, "room unavailable", http.StatusInternalServerError)
		return
	}
	if room == nil {
		http.Error(w, "no such room", http.StatusNotFound)
		return
	}
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	s.mu.Lock()
	s.clients++
	n := s.clients
	s.mu.Unlock()
	c := newClient(newID(), n, conn)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go c.writeLoop(ctx)
	go func() {
		<-c.closed
		cancel()
	}()
	c.readLoop(ctx, room)
	room.leave <- c
	_ = conn.Close(websocket.StatusNormalClosure, "")
}

// Shutdown stops every room and waits until each has flushed unsaved edits.
func (s *Server) Shutdown() {
	s.mu.Lock()
	for id, done := range s.stop {
		close(done)
		delete(s.stop, id)
		delete(s.rooms, id)
	}
	s.mu.Unlock()
	s.wg.Wait()
}
