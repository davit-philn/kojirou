package web

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

// SeriesMeta remembers where the covers of a library series come from.
type SeriesMeta struct {
	// MangaDexID is the MangaDex series the volumes' covers are taken from.
	MangaDexID string `json:"mangadexId,omitempty"`
	// CoverPage is a series page that shows each volume with its cover.
	CoverPage string `json:"coverPage,omitempty"`
}

// MetaStore keeps SeriesMeta by library series name in a JSON file.
type MetaStore struct {
	path string
	mu   sync.Mutex
}

func NewMetaStore(path string) *MetaStore { return &MetaStore{path: path} }

func (s *MetaStore) load() (map[string]SeriesMeta, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]SeriesMeta{}, nil
	} else if err != nil {
		return nil, err
	}
	m := map[string]SeriesMeta{}
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}

	return m, nil
}

func (s *MetaStore) Get(series string) (SeriesMeta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.load()

	return m[series], err
}

func (s *MetaStore) Save(series string, meta SeriesMeta) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.load()
	if err != nil {
		return err
	}
	m[series] = meta
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".libmeta-*")
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
