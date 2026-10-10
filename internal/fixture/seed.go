package fixture

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/torfstack/grove/internal/drive"
	"os"
	"sort"
)

// Seed requires the caller to hold the run lock before opening a token session.
func Seed(ctx context.Context, api drive.API, v Verified, dir string) (Run, error) {
	paths, err := manifestPaths(v.Manifest)
	if err != nil {
		return Run{}, err
	}
	payloads := map[string][]byte{}
	for _, e := range v.Manifest.Entries {
		if e.Kind == "file" {
			data, err := Payload(v, e)
			if err != nil {
				return Run{}, err
			}
			sum := sha256.Sum256(data)
			if int64(len(data)) != e.Size || hex.EncodeToString(sum[:]) != e.SHA256 {
				return Run{}, errors.New("fixture payload changed after validation")
			}
			payloads[e.ID] = data
		}
	}
	if err = os.Mkdir(dir, 0700); err != nil {
		return Run{}, errors.New("fixture run directory must be new")
	}
	nonce := make([]byte, 16)
	if _, err = rand.Read(nonce); err != nil {
		return Run{}, errors.New("cannot create run identity")
	}
	run := Run{Version: 1, RunID: hex.EncodeToString(nonce), Manifest: v.Manifest}
	entries := append([]Entry(nil), v.Manifest.Entries...)
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Parent == "" {
			return true
		}
		if entries[j].Parent == "" {
			return false
		}
		return paths[entries[i].ID] < paths[entries[j].ID]
	})
	ids := map[string]string{}
	for _, e := range entries {
		if err = ctx.Err(); err != nil {
			return run, err
		}
		parent := ids[e.Parent]
		if e.Parent != "" && parent == "" {
			return run, errors.New("fixture parent has not been created")
		}
		run.Objects = append(run.Objects, Object{LogicalID: e.ID, ParentID: parent, Status: "pending"})
		if err = saveRun(dir, run); err != nil {
			return run, err
		}
		input := drive.Create{Name: e.Name, ParentID: parent, Properties: map[string]string{"grove_run": run.RunID, "grove_entry": e.ID}, MIMEType: drive.FolderMIME}
		var content *bytes.Reader
		if e.Kind == "file" {
			input.MIMEType = "application/octet-stream"
			content = bytes.NewReader(payloads[e.ID])
		}
		var f drive.File
		if content == nil {
			f, err = api.Create(ctx, input, nil)
		} else {
			f, err = api.Create(ctx, input, content)
		}
		if err != nil {
			return run, errors.New("fixture create failed; inspect the saved run before cleanup")
		}
		if f.ID == "" {
			return run, errors.New("fixture create outcome is uncertain")
		}
		ids[e.ID] = f.ID
		last := len(run.Objects) - 1
		run.Objects[last].RemoteID = f.ID
		run.Objects[last].Status = "created"
		if err = saveRun(dir, run); err != nil {
			return run, err
		}
	}
	return run, nil
}
