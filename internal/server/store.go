package server

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/aamodbhatt/nandbox/internal/circuit"
	bolt "go.etcd.io/bbolt"
)

var roomsBucket = []byte("rooms")

// Store persists room snapshots (design + sequence number) in a bbolt file.
type Store struct{ db *bolt.DB }

type savedRoom struct {
	Template string          `json:"template"`
	Seq      uint64          `json:"seq"`
	Design   *circuit.Design `json:"design"`
}

// OpenStore opens (or creates) the database file.
func OpenStore(path string) (*Store, error) {
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: 2 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("open store %s: %w", path, err)
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists(roomsBucket)
		return err
	}); err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// Save writes a room snapshot.
func (s *Store) Save(id, tmpl string, d *circuit.Design, seq uint64) error {
	b, err := json.Marshal(savedRoom{Template: tmpl, Seq: seq, Design: d})
	if err != nil {
		return err
	}
	return s.db.Update(func(tx *bolt.Tx) error { return tx.Bucket(roomsBucket).Put([]byte(id), b) })
}

// Load reads a room snapshot; ok is false if the room was never saved.
func (s *Store) Load(id string) (tmpl string, d *circuit.Design, seq uint64, ok bool, err error) {
	var raw []byte
	err = s.db.View(func(tx *bolt.Tx) error {
		if v := tx.Bucket(roomsBucket).Get([]byte(id)); v != nil {
			raw = append([]byte(nil), v...)
		}
		return nil
	})
	if err != nil || raw == nil {
		return "", nil, 0, false, err
	}
	var sr savedRoom
	if err := json.Unmarshal(raw, &sr); err != nil {
		return "", nil, 0, false, fmt.Errorf("room %s: corrupt snapshot: %w", id, err)
	}
	if err := sr.Design.Validate(); err != nil {
		return "", nil, 0, false, fmt.Errorf("room %s: invalid snapshot: %w", id, err)
	}
	return sr.Template, sr.Design, sr.Seq, true, nil
}
