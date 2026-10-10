package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"

	"github.com/leotaku/kojirou/cmd/formats/selector"
)

var sourceName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,39}$`)

// reservedSources are names that cannot be used for saved sources.
var reservedSources = map[string]bool{"mangadex": true}

// SourceStore keeps named selector configurations in a JSON file.
type SourceStore struct {
	path string
	mu   sync.Mutex
}

func NewSourceStore(path string) *SourceStore {
	return &SourceStore{path: path}
}

type SavedSource struct {
	Name   string                `json:"name"`
	Config selector.SourceConfig `json:"config"`
}

func (s *SourceStore) load() (map[string]selector.SourceConfig, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]selector.SourceConfig{}, nil
	} else if err != nil {
		return nil, err
	}
	m := map[string]selector.SourceConfig{}
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("%v: %w", s.path, err)
	}

	return m, nil
}

func (s *SourceStore) List() ([]SavedSource, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.load()
	if err != nil {
		return nil, err
	}
	list := make([]SavedSource, 0, len(m))
	for name, cfg := range m {
		list = append(list, SavedSource{name, cfg})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })

	return list, nil
}

func (s *SourceStore) Get(name string) (selector.SourceConfig, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.load()
	if err != nil {
		return selector.SourceConfig{}, false, err
	}
	cfg, ok := m[name]

	return cfg, ok, nil
}

func (s *SourceStore) Save(name string, cfg selector.SourceConfig) error {
	if !sourceName.MatchString(name) || reservedSources[name] {
		return fmt.Errorf("invalid source name %q: use letters, digits, - and _ (max 40)", name)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.load()
	if err != nil {
		return err
	}
	m[name] = cfg

	return s.write(m)
}

func (s *SourceStore) Delete(name string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.load()
	if err != nil {
		return false, err
	}
	if _, ok := m[name]; !ok {
		return false, nil
	}
	delete(m, name)

	return true, s.write(m)
}

func (s *SourceStore) write(m map[string]selector.SourceConfig) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".sources-*")
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
