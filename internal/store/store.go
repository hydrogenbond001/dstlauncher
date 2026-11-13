// Package store keeps one directory per frp profile under profiles/.
package store

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

type Profile struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Kind      string    `json:"kind"` // "client" or "server"
	CreatedAt time.Time `json:"createdAt"`
}

type Store struct {
	mu   sync.RWMutex
	dir  string
	list []Profile
}

func New(dir string) *Store {
	return &Store{dir: dir}
}

func (s *Store) Load() error {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.list = s.list[:0]
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.dir, e.Name(), "meta.json"))
		if err != nil {
			log.Printf("skip profile %s: %v", e.Name(), err)
			continue
		}
		var p Profile
		if err := json.Unmarshal(data, &p); err != nil {
			log.Printf("skip profile %s: %v", e.Name(), err)
			continue
		}
		if p.ID == "" {
			p.ID = e.Name()
		}
		s.list = append(s.list, p)
	}
	s.sortLocked()
	return nil
}

func (s *Store) List() []Profile {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Profile, len(s.list))
	copy(out, s.list)
	return out
}

func (s *Store) Get(id string) (Profile, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, p := range s.list {
		if p.ID == id {
			return p, true
		}
	}
	return Profile{}, false
}

func (s *Store) Dir(id string) string {
	return filepath.Join(s.dir, id)
}

func (s *Store) Add(p Profile) error {
	dir := s.Dir(p.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(dir, "meta.json"), append(data, '\n')); err != nil {
		return err
	}
	s.mu.Lock()
	s.list = append(s.list, p)
	s.sortLocked()
	s.mu.Unlock()
	return nil
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	idx := -1
	for i, p := range s.list {
		if p.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		s.mu.Unlock()
		return os.ErrNotExist
	}
	s.list = append(s.list[:idx], s.list[idx+1:]...)
	s.mu.Unlock()
	return os.RemoveAll(s.Dir(id))
}

// UniqueID turns a display name into a directory-safe id that is not taken yet.
func (s *Store) UniqueID(name string) string {
	base := slug(name)
	s.mu.RLock()
	defer s.mu.RUnlock()
	taken := make(map[string]bool, len(s.list))
	for _, p := range s.list {
		taken[p.ID] = true
	}
	if !taken[base] {
		return base
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d", base, i)
		if !taken[candidate] {
			return candidate
		}
	}
}

func (s *Store) sortLocked() {
	sort.Slice(s.list, func(i, j int) bool {
		return strings.ToLower(s.list[i].Name) < strings.ToLower(s.list[j].Name)
	})
}

func slug(name string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
			lastDash = false
		case unicode.IsSpace(r), r == '-', r == '.':
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 40 {
		out = strings.Trim(out[:40], "-")
	}
	if out == "" {
		out = "profile"
	}
	return out
}

func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
