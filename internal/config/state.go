package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const maxRecent = 10

// State is app-owned data that persists between runs.
type State struct {
	Recent []string `json:"recent"`
}

// LoadState reads the state file. A missing file yields an empty state.
func LoadState(path string) (State, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return State{}, nil
	}
	if err != nil {
		return State{}, err
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return State{}, fmt.Errorf("reading %s: %w", path, err)
	}
	return s, nil
}

// AddRecent moves node to the front of the recent nodes.
func (s *State) AddRecent(node string) {
	recent := []string{node}
	for _, n := range s.Recent {
		if n != node && len(recent) < maxRecent {
			recent = append(recent, n)
		}
	}
	s.Recent = recent
}

// Save writes the state file atomically.
func (s State) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
