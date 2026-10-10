package fixture

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/torfstack/grove/internal/privatefs"
	"os"
	"path/filepath"
)

type Entry struct {
	ID      string `json:"id"`
	Parent  string `json:"parent,omitempty"`
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Payload string `json:"payload,omitempty"`
	SHA256  string `json:"sha256,omitempty"`
	Size    int64  `json:"size,omitempty"`
}
type Manifest struct {
	Version int     `json:"version"`
	Entries []Entry `json:"entries"`
}
type Verified struct {
	Manifest Manifest
	BaseDir  string
}
type Report struct{ Files, Directories int }

func LoadManifest(path string) (Verified, error) {
	var m Manifest
	if err := privatefs.ReadJSON(path, &m); err != nil {
		return Verified{}, err
	}
	if _, err := manifestPaths(m); err != nil {
		return Verified{}, err
	}
	base, err := privatefs.Canonical(filepath.Dir(path))
	if err != nil {
		return Verified{}, err
	}
	v := Verified{Manifest: m, BaseDir: base}
	for _, e := range m.Entries {
		if e.Kind == "file" {
			data, err := Payload(v, e)
			if err != nil {
				return Verified{}, err
			}
			sum := sha256.Sum256(data)
			if int64(len(data)) != e.Size || hex.EncodeToString(sum[:]) != e.SHA256 {
				return Verified{}, errors.New("fixture payload size or hash mismatch")
			}
		}
	}
	return v, nil
}
func Payload(v Verified, e Entry) ([]byte, error) {
	if e.Payload == "" || filepath.IsAbs(e.Payload) || !filepath.IsLocal(e.Payload) {
		return nil, errors.New("unsafe fixture payload path")
	}
	p := filepath.Join(v.BaseDir, e.Payload)
	if err := privatefs.NoSymlinks(p); err != nil {
		return nil, err
	}
	s, err := os.Stat(p)
	if err != nil || !s.Mode().IsRegular() {
		return nil, errors.New("fixture payload must be a regular file")
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, errors.New("cannot read fixture payload")
	}
	return data, nil
}
func manifestPaths(m Manifest) (map[string]string, error) {
	if m.Version != 1 || len(m.Entries) == 0 {
		return nil, errors.New("unsupported fixture manifest")
	}
	entries := map[string]Entry{}
	siblings := map[string]bool{}
	root := ""
	for _, e := range m.Entries {
		if e.ID == "" || !privatefs.SafeName(e.Name) || e.Size < 0 || (e.Kind != "file" && e.Kind != "folder") {
			return nil, errors.New("invalid fixture entry")
		}
		if _, ok := entries[e.ID]; ok {
			return nil, errors.New("duplicate fixture identity")
		}
		entries[e.ID] = e
		key := e.Parent + "\x00" + e.Name
		if siblings[key] {
			return nil, errors.New("duplicate fixture name")
		}
		siblings[key] = true
		if e.Parent == "" {
			if root != "" || e.Kind != "folder" {
				return nil, errors.New("invalid fixture root")
			}
			root = e.ID
		}
	}
	if root == "" {
		return nil, errors.New("missing fixture root")
	}
	paths := map[string]string{root: "."}
	visiting := map[string]bool{}
	var visit func(string) (string, error)
	visit = func(id string) (string, error) {
		if p, ok := paths[id]; ok {
			return p, nil
		}
		e, ok := entries[id]
		parent, found := entries[e.Parent]
		if !ok || !found || parent.Kind != "folder" || visiting[id] {
			return "", errors.New("invalid fixture parent graph")
		}
		visiting[id] = true
		p, err := visit(e.Parent)
		if err != nil {
			return "", err
		}
		visiting[id] = false
		p = filepath.Join(p, e.Name)
		paths[id] = p
		return p, nil
	}
	for id := range entries {
		if _, err := visit(id); err != nil {
			return nil, err
		}
	}
	return paths, nil
}
