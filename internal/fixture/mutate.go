package fixture

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/torfstack/grove/internal/drive"
	"io"
	"reflect"
	"sort"
)

type Change struct {
	Target  Manifest  `json:"target"`
	Pending *Mutation `json:"pending,omitempty"`
}
type Mutation struct {
	Before   Entry  `json:"before"`
	After    Entry  `json:"after"`
	Object   Object `json:"object"`
	ParentID string `json:"parent_id"`
}

func verifiedPayloads(v Verified) (map[string][]byte, error) {
	if _, err := manifestPaths(v.Manifest); err != nil {
		return nil, err
	}
	out := map[string][]byte{}
	for _, e := range v.Manifest.Entries {
		if e.Kind != "file" {
			continue
		}
		b, err := Payload(v, e)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(b)
		if int64(len(b)) != e.Size || hex.EncodeToString(sum[:]) != e.SHA256 {
			return nil, errors.New("fixture payload changed")
		}
		out[e.ID] = b
	}
	return out, nil
}

// ApplyChanges requires the caller to hold the run lock before opening a token session.
func ApplyChanges(ctx context.Context, api drive.MutationAPI, dir string, target Verified) error {
	payloads, err := verifiedPayloads(target)
	if err != nil {
		return err
	}
	if _, err = Inspect(ctx, api, dir); err != nil {
		return err
	}
	r, err := LoadRun(dir)
	if err != nil {
		return err
	}
	old := runEntries(r)
	wanted := map[string]Entry{}
	for _, e := range target.Manifest.Entries {
		wanted[e.ID] = e
	}
	for _, e := range r.Manifest.Entries {
		next, ok := wanted[e.ID]
		if !ok || next.Kind != e.Kind || (e.Parent == "" && !reflect.DeepEqual(e, next)) {
			return errors.New("fixture deletion, type or root changes are unsupported")
		}
	}
	if r.Change != nil && !reflect.DeepEqual(r.Change.Target, target.Manifest) {
		return errors.New("different fixture changes are pending")
	}
	r.Change = &Change{Target: target.Manifest}
	if err = saveRun(dir, r); err != nil {
		return err
	}
	paths, _ := manifestPaths(target.Manifest)
	entries := append([]Entry(nil), target.Manifest.Entries...)
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Parent == "" {
			return true
		}
		if entries[j].Parent == "" {
			return false
		}
		return paths[entries[i].ID] < paths[entries[j].ID]
	})
	for _, entry := range entries {
		if err = ctx.Err(); err != nil {
			return err
		}
		objects := map[string]Object{}
		for _, o := range r.Objects {
			objects[o.LogicalID] = o
		}
		parent := objects[entry.Parent].RemoteID
		if entry.Parent != "" && parent == "" {
			return errors.New("fixture target parent missing")
		}
		before, exists := old[entry.ID]
		if !exists {
			r.Manifest.Entries = append(r.Manifest.Entries, entry)
			if _, err = createRecorded(ctx, api, dir, &r, entry, parent, payloads[entry.ID]); err != nil {
				return err
			}
			old[entry.ID] = entry
			continue
		}
		if reflect.DeepEqual(before, entry) {
			continue
		}
		object := objects[entry.ID]
		r.Change.Pending = &Mutation{Before: before, After: entry, Object: object, ParentID: parent}
		if err = saveRun(dir, r); err != nil {
			return err
		}
		update := drive.Update{Name: entry.Name}
		if object.ParentID != parent {
			update.AddParent = parent
			update.RemoveParent = object.ParentID
		}
		var reader io.Reader
		if entry.Kind == "file" && (before.Size != entry.Size || before.SHA256 != entry.SHA256) {
			update.ContentChanged = true
			reader = bytes.NewReader(payloads[entry.ID])
		}
		if _, err = api.Update(ctx, object.RemoteID, update, reader); err != nil {
			return errors.New("fixture update uncertain; inspect saved run")
		}
		if err = reconcileMutation(ctx, api, dir, &r); err != nil {
			return err
		}
		if r.Change.Pending != nil || !reflect.DeepEqual(runEntries(r)[entry.ID], entry) {
			return errors.New("fixture update was not confirmed")
		}
		old[entry.ID] = entry
	}
	if _, err = membership(ctx, api, r); err != nil {
		return err
	}
	r.Manifest = target.Manifest
	r.Change = nil
	return saveRun(dir, r)
}
func matchesMutation(ctx context.Context, api drive.API, f drive.File, entry Entry) (bool, error) {
	if f.Name != entry.Name || (f.MIMEType == drive.FolderMIME) != (entry.Kind == "folder") {
		return false, nil
	}
	if entry.Kind == "folder" {
		return true, nil
	}
	if !f.HasSize || f.Size != entry.Size {
		return false, nil
	}
	hash := sha256.New()
	counter := &countWriter{writer: hash}
	if err := api.Download(ctx, f.ID, counter); err != nil {
		return false, err
	}
	return counter.count == entry.Size && hex.EncodeToString(hash.Sum(nil)) == entry.SHA256, nil
}
func reconcileMutation(ctx context.Context, api drive.API, dir string, r *Run) error {
	if r.Change == nil || r.Change.Pending == nil {
		return nil
	}
	p := r.Change.Pending
	f, err := api.Get(ctx, p.Object.RemoteID)
	if err != nil {
		return err
	}
	afterObject := p.Object
	afterObject.ParentID = p.ParentID
	if f.Trashed || !f.OwnedByMe || f.DriveID != "" || f.Properties["grove_run"] != r.RunID || f.Properties["grove_entry"] != p.Object.LogicalID {
		return errors.New("fixture update ownership changed")
	}
	after, err := matchesMutation(ctx, api, f, p.After)
	if err != nil {
		return err
	}
	if after && owned(f, *r, afterObject) {
		for i := range r.Manifest.Entries {
			if r.Manifest.Entries[i].ID == p.After.ID {
				r.Manifest.Entries[i] = p.After
			}
		}
		for i := range r.Objects {
			if r.Objects[i].LogicalID == p.After.ID {
				r.Objects[i] = afterObject
			}
		}
	} else {
		before, err := matchesMutation(ctx, api, f, p.Before)
		if err != nil {
			return err
		}
		if !before || !owned(f, *r, p.Object) {
			return errors.New("ambiguous fixture update; preserved for inspection")
		}
	}
	r.Change.Pending = nil
	return saveRun(dir, *r)
}
