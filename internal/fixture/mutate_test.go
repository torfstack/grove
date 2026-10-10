package fixture

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"github.com/torfstack/grove/internal/drive"
	"io"
	"reflect"
	"testing"
)

type mutationFake struct {
	*fakeDrive
	updates   int
	uncertain bool
}

func (f *mutationFake) Update(ctx context.Context, id string, u drive.Update, content io.Reader) (drive.File, error) {
	r, err := LoadRun(f.runDir)
	if err != nil || r.Change == nil || r.Change.Pending == nil {
		return drive.File{}, errors.New("update intent missing")
	}
	v := f.files[id]
	v.Name = u.Name
	if u.AddParent != "" {
		v.Parents = []string{u.AddParent}
	}
	v.Version = "2"
	if content != nil {
		b, err := io.ReadAll(content)
		if err != nil {
			return drive.File{}, err
		}
		f.content[id] = b
		v.Size = int64(len(b))
		sum := md5.Sum(b)
		v.MD5 = hex.EncodeToString(sum[:])
	}
	f.files[id] = v
	f.updates++
	if f.uncertain {
		return drive.File{}, errors.New("uncertain update")
	}
	return v, nil
}
func changedFixture(t *testing.T) Verified {
	v := baseline(t)
	v.Manifest.Entries = append([]Entry(nil), v.Manifest.Entries...)
	for i := range v.Manifest.Entries {
		e := &v.Manifest.Entries[i]
		if e.Kind == "file" && e.Size > 0 {
			e.Name = "renamed-" + e.Name
			break
		}
	}
	v.Manifest.Entries = append(v.Manifest.Entries, Entry{ID: "added", Parent: "root", Name: "added", Kind: "folder"})
	return v
}
func TestFixtureChangesOwnedOnly(t *testing.T) {
	for _, mode := range []string{"unknown", "ownership", "parent", "incomplete"} {
		t.Run(mode, func(t *testing.T) {
			base, dir, r := seedFake(t)
			api := &mutationFake{fakeDrive: base}
			before := map[string]drive.File{}
			for id, f := range base.files {
				before[id] = f
			}
			switch mode {
			case "unknown":
				base.files["unknown"] = drive.File{ID: "unknown", Parents: []string{r.Objects[0].RemoteID}}
			case "ownership":
				f := base.files[r.Objects[1].RemoteID]
				f.Properties = map[string]string{}
				base.files[f.ID] = f
			case "parent":
				f := base.files[r.Objects[1].RemoteID]
				f.Parents = []string{"outside"}
				base.files[f.ID] = f
			case "incomplete":
				base.failList = true
			}
			if err := ApplyChanges(context.Background(), api, dir, changedFixture(t)); err == nil || api.updates != 0 {
				t.Fatal("unsafe mutation accepted")
			}
			if len(base.files) < len(before) {
				t.Fatal("data removed")
			}
		})
	}
}
func TestFixtureChangesAndReconciliation(t *testing.T) {
	for _, uncertain := range []bool{false, true} {
		t.Run(map[bool]string{false: "confirmed", true: "uncertain"}[uncertain], func(t *testing.T) {
			base, dir, _ := seedFake(t)
			api := &mutationFake{fakeDrive: base, uncertain: uncertain}
			target := changedFixture(t)
			err := ApplyChanges(context.Background(), api, dir, target)
			if uncertain {
				if err == nil {
					t.Fatal("ambiguous update accepted")
				}
				api.uncertain = false
				if _, err = Inspect(context.Background(), api, dir); err != nil {
					t.Fatal("reconcile", err)
				}
				err = ApplyChanges(context.Background(), api, dir, target)
			}
			if err != nil {
				t.Fatal(err)
			}
			r, err := LoadRun(dir)
			if err != nil || !reflect.DeepEqual(r.Manifest, target.Manifest) {
				t.Fatal("target manifest not committed", err)
			}
			if _, err = Inspect(context.Background(), api, dir); err != nil {
				t.Fatal(err)
			}
			if err = Cleanup(context.Background(), api, dir); err != nil {
				t.Fatal(err)
			}
		})
	}
}
