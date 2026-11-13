package store

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DSTProfile registers an existing Don't Starve Together cluster directory.
// The panel never generates or rewrites the cluster layout: it reads the
// config files that are already there and launches the cluster's bat.
type DSTProfile struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	ClusterDir string    `json:"clusterDir"`
	LaunchArgs []string  `json:"launchArgs,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
}

type DSTStore struct {
	mu   sync.RWMutex
	dir  string
	list []DSTProfile
}

func NewDST(dir string) *DSTStore {
	return &DSTStore{dir: dir}
}

func (s *DSTStore) Load() error {
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
			log.Printf("skip dst instance %s: %v", e.Name(), err)
			continue
		}
		var p DSTProfile
		if err := json.Unmarshal(data, &p); err != nil {
			log.Printf("skip dst instance %s: %v", e.Name(), err)
			continue
		}
		if p.ID == "" {
			p.ID = e.Name()
		}
		if p.ClusterDir == "" {
			log.Printf("skip dst instance %s: no clusterDir (please recreate)", e.Name())
			continue
		}
		s.list = append(s.list, p)
	}
	s.sortLocked()
	return nil
}

func (s *DSTStore) List() []DSTProfile {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]DSTProfile, len(s.list))
	copy(out, s.list)
	return out
}

func (s *DSTStore) Get(id string) (DSTProfile, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, p := range s.list {
		if p.ID == id {
			return p, true
		}
	}
	return DSTProfile{}, false
}

func (s *DSTStore) Dir(id string) string {
	return filepath.Join(s.dir, id)
}

func (s *DSTStore) Add(p DSTProfile) error {
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

func (s *DSTStore) Delete(id string) error {
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

// UniqueID turns a display name into an unused directory-safe id.
func (s *DSTStore) UniqueID(name string) string {
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
		candidate := base + "-" + strconv.Itoa(i)
		if !taken[candidate] {
			return candidate
		}
	}
}

func (s *DSTStore) sortLocked() {
	sort.Slice(s.list, func(i, j int) bool {
		return strings.ToLower(s.list[i].Name) < strings.ToLower(s.list[j].Name)
	})
}
