package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"meldnet/internal/securefs"
)

type Store struct{ dir string }

func Open(dir string) (*Store, error) {
	if err := securefs.Directory(dir); err != nil {
		return nil, err
	}
	return &Store{dir: dir}, nil
}

func (s *Store) Load() (*Node, error) {
	data, err := securefs.Read(filepath.Join(s.dir, "node.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var n Node
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&n); err != nil {
		return nil, errors.New("invalid stored configuration JSON")
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return nil, errors.New("trailing data in stored configuration")
	}
	if err := n.Validate(); err != nil {
		return nil, err
	}
	return &n, nil
}

func (s *Store) Save(n *Node) error {
	if err := n.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(n, "", "  ")
	if err != nil {
		return err
	}
	return securefs.Write(filepath.Join(s.dir, "node.json"), append(data, '\n'))
}
