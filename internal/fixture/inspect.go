package fixture

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/torfstack/grove/internal/drive"
	"io"
)

func runEntries(r Run) map[string]Entry {
	m := map[string]Entry{}
	for _, e := range r.Manifest.Entries {
		m[e.ID] = e
	}
	return m
}
func owned(f drive.File, r Run, o Object) bool {
	return f.OwnedByMe && f.DriveID == "" && f.Properties["grove_run"] == r.RunID && f.Properties["grove_entry"] == o.LogicalID && (o.ParentID == "" || (len(f.Parents) == 1 && f.Parents[0] == o.ParentID))
}
func reconcile(ctx context.Context, api drive.API, dir string, r *Run) error {
	for i, o := range r.Objects {
		if o.Status != "pending" {
			continue
		}
		q := "trashed = false and appProperties has { key='grove_run' and value='" + drive.EscapeQuery(r.RunID) + "' } and appProperties has { key='grove_entry' and value='" + drive.EscapeQuery(o.LogicalID) + "' }"
		files, err := drive.ListAll(ctx, api, q)
		if err != nil {
			return err
		}
		if len(files) > 1 {
			return errors.New("ambiguous fixture create; retained for inspection")
		}
		if len(files) == 0 {
			continue
		}
		if !owned(files[0], *r, o) {
			return errors.New("fixture reconciliation ownership mismatch")
		}
		r.Objects[i].RemoteID = files[0].ID
		r.Objects[i].Status = "created"
		if err = saveRun(dir, *r); err != nil {
			return err
		}
	}
	return nil
}
func membership(ctx context.Context, api drive.API, r Run) (map[string]drive.File, error) {
	expected := runEntries(r)
	known := map[string]bool{}
	for _, o := range r.Objects {
		if o.RemoteID != "" {
			known[o.RemoteID] = true
		}
	}
	result := map[string]drive.File{}
	for _, o := range r.Objects {
		if o.Status == "trashed" || o.RemoteID == "" {
			continue
		}
		f, err := api.Get(ctx, o.RemoteID)
		if errors.Is(err, drive.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if f.Trashed {
			continue
		}
		if !owned(f, r, o) {
			return nil, errors.New("fixture ownership changed")
		}
		e, ok := expected[o.LogicalID]
		if !ok || f.Name != e.Name {
			return nil, errors.New("fixture metadata mismatch")
		}
		if (e.Kind == "folder") != (f.MIMEType == drive.FolderMIME) {
			return nil, errors.New("fixture type mismatch")
		}
		result[o.LogicalID] = f
		if e.Kind == "folder" {
			children, err := drive.ListAll(ctx, api, drive.ChildrenQuery(f.ID))
			if err != nil {
				return nil, err
			}
			for _, child := range children {
				if !known[child.ID] {
					return nil, errors.New("unrecorded fixture descendant; cleanup refused")
				}
			}
		}
	}
	return result, nil
}

// Inspect requires the caller to hold the run lock before opening a token session.
func Inspect(ctx context.Context, api drive.API, dir string) (Report, error) {
	r, err := LoadRun(dir)
	if err != nil {
		return Report{}, err
	}
	if err = reconcile(ctx, api, dir, &r); err != nil {
		return Report{}, err
	}
	files, err := membership(ctx, api, r)
	if err != nil {
		return Report{}, err
	}
	if len(files) != len(r.Manifest.Entries) {
		return Report{}, errors.New("fixture is incomplete")
	}
	report := Report{}
	for _, e := range r.Manifest.Entries {
		f := files[e.ID]
		if e.Kind == "folder" {
			if e.Parent != "" {
				report.Directories++
			}
			continue
		}
		if !f.HasSize || f.Size != e.Size {
			return Report{}, errors.New("fixture remote size mismatch")
		}
		hash := sha256.New()
		counter := &countWriter{writer: hash}
		if err = api.Download(ctx, f.ID, counter); err != nil {
			return Report{}, err
		}
		if counter.count != e.Size || hex.EncodeToString(hash.Sum(nil)) != e.SHA256 {
			return Report{}, errors.New("fixture remote content mismatch")
		}
		report.Files++
	}
	return report, nil
}

type countWriter struct {
	writer io.Writer
	count  int64
}

func (w *countWriter) Write(p []byte) (int, error) {
	n, err := w.writer.Write(p)
	w.count += int64(n)
	return n, err
}
