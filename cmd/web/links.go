package web

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Link ties a MangaDex series to a page on a saved source, whose chapters
// fill the gaps MangaDex has.
type Link struct {
	Source string `json:"source"`
	URL    string `json:"url"`
}

// LinkStore keeps links in a JSON file, keyed by MangaDex series ID.
type LinkStore struct {
	path string
	mu   sync.Mutex
}

func NewLinkStore(path string) *LinkStore { return &LinkStore{path: path} }

func (s *LinkStore) load() (map[string]Link, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]Link{}, nil
	} else if err != nil {
		return nil, err
	}
	m := map[string]Link{}
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}

	return m, nil
}

func (s *LinkStore) All() (map[string]Link, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.load()
}

func (s *LinkStore) Save(id string, link Link) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.load()
	if err != nil {
		return err
	}
	m[strings.ToLower(id)] = link

	return s.write(m)
}

func (s *LinkStore) Delete(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.load()
	if err != nil {
		return false, err
	}
	id = strings.ToLower(id)
	if _, ok := m[id]; !ok {
		return false, nil
	}
	delete(m, id)

	return true, s.write(m)
}

func (s *LinkStore) write(m map[string]Link) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".links-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) //nolint:errcheck
	if _, err := tmp.Write(data); err != nil {
		tmp.Close() //nolint:errcheck
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	return os.Rename(tmp.Name(), s.path)
}
