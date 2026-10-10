package fixture

import (
	"errors"
	"github.com/torfstack/grove/internal/privatefs"
	"path/filepath"
)

type Run struct {
	Version  int      `json:"version"`
	RunID    string   `json:"run_id"`
	Manifest Manifest `json:"manifest"`
	Objects  []Object `json:"objects"`
}
type Object struct {
	LogicalID string `json:"logical_id"`
	RemoteID  string `json:"remote_id,omitempty"`
	ParentID  string `json:"parent_id,omitempty"`
	Status    string `json:"status"`
}

func saveRun(dir string, r Run) error { return privatefs.WriteJSON(filepath.Join(dir, "run.json"), r) }
func LoadRun(dir string) (Run, error) {
	var r Run
	if err := privatefs.ReadJSON(filepath.Join(dir, "run.json"), &r); err != nil {
		return Run{}, err
	}
	if r.Version != 1 || len(r.RunID) != 32 {
		return Run{}, errors.New("invalid fixture run")
	}
	if _, err := manifestPaths(r.Manifest); err != nil {
		return Run{}, err
	}
	logical := map[string]bool{}
	remote := map[string]bool{}
	for _, o := range r.Objects {
		if logical[o.LogicalID] || o.LogicalID == "" {
			return Run{}, errors.New("invalid fixture run entries")
		}
		logical[o.LogicalID] = true
		if o.RemoteID != "" {
			if remote[o.RemoteID] {
				return Run{}, errors.New("duplicate fixture remote identity")
			}
			remote[o.RemoteID] = true
		}
		if o.Status != "pending" && o.Status != "created" && o.Status != "trashed" {
			return Run{}, errors.New("invalid fixture run status")
		}
	}
	return r, nil
}
