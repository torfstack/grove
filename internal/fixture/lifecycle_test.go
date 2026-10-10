package fixture

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"github.com/torfstack/grove/internal/drive"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

type fakeDrive struct {
	files                map[string]drive.File
	content              map[string][]byte
	failCreate, failList bool
	trash                []string
	runDir               string
}

func newFake() *fakeDrive {
	return &fakeDrive{files: map[string]drive.File{}, content: map[string][]byte{}}
}
func (f *fakeDrive) Get(_ context.Context, id string) (drive.File, error) {
	v, ok := f.files[id]
	if !ok {
		return drive.File{}, drive.ErrNotFound
	}
	return v, nil
}
func (f *fakeDrive) List(_ context.Context, q, token string) (drive.Page, error) {
	if f.failList {
		return drive.Page{Incomplete: true}, nil
	}
	var out []drive.File
	for _, v := range f.files {
		if v.Trashed {
			continue
		}
		if strings.Contains(q, "in parents") {
			if len(v.Parents) > 0 && strings.Contains(q, "'"+v.Parents[0]+"'") {
				out = append(out, v)
			}
		} else if strings.Contains(q, v.Properties["grove_run"]) && strings.Contains(q, "value='"+v.Properties["grove_entry"]+"'") {
			out = append(out, v)
		}
	}
	return drive.Page{Files: out}, nil
}
func (f *fakeDrive) Download(_ context.Context, id string, w io.Writer) error {
	_, err := w.Write(f.content[id])
	return err
}
func (f *fakeDrive) Create(_ context.Context, c drive.Create, r io.Reader) (drive.File, error) {
	if f.runDir != "" {
		record, err := LoadRun(f.runDir)
		if err != nil {
			return drive.File{}, err
		}
		last := record.Objects[len(record.Objects)-1]
		if last.Status != "pending" || last.LogicalID != c.Properties["grove_entry"] {
			return drive.File{}, errors.New("intent not persisted")
		}
		for _, o := range record.Objects[:len(record.Objects)-1] {
			if o.Status != "created" {
				return drive.File{}, errors.New("prior create not persisted")
			}
		}
	}
	id := strconv.Itoa(len(f.files) + 1)
	v := drive.File{ID: id, Name: c.Name, MIMEType: c.MIMEType, Properties: c.Properties, OwnedByMe: true, Version: "1", CanDownload: true}
	if c.ParentID != "" {
		v.Parents = []string{c.ParentID}
	}
	if r != nil {
		data, err := io.ReadAll(r)
		if err != nil {
			return drive.File{}, err
		}
		f.content[id] = data
		sum := md5.Sum(data)
		v.MD5 = hex.EncodeToString(sum[:])
		v.Size = int64(len(data))
		v.HasSize = true
	}
	f.files[id] = v
	if f.failCreate {
		return drive.File{}, errors.New("uncertain create")
	}
	return v, nil
}
func (f *fakeDrive) Trash(_ context.Context, id string) error {
	v, ok := f.files[id]
	if !ok {
		return drive.ErrNotFound
	}
	v.Trashed = true
	f.files[id] = v
	f.trash = append(f.trash, id)
	return nil
}
func seedFake(t *testing.T) (*fakeDrive, string, Run) {
	t.Helper()
	api := newFake()
	dir := filepath.Join(t.TempDir(), "run")
	api.runDir = dir
	run, err := Seed(context.Background(), api, baseline(t), dir)
	if err != nil {
		t.Fatal(err)
	}
	return api, dir, run
}
func TestSeedFreshRun(t *testing.T) {
	api, dir, run := seedFake(t)
	if len(run.Objects) != 9 || len(api.files) != 9 {
		t.Fatal("wrong fixture membership")
	}
	if _, err := Seed(context.Background(), api, baseline(t), dir); err == nil {
		t.Fatal("reused run")
	}
}
func TestSeedPersistsBeforeNextCreate(t *testing.T) { seedFake(t) }
func TestAmbiguousRootReconciliation(t *testing.T) {
	api := newFake()
	api.failCreate = true
	dir := filepath.Join(t.TempDir(), "run")
	_, err := Seed(context.Background(), api, baseline(t), dir)
	if err == nil || len(api.files) != 1 {
		t.Fatal("uncertain create retried")
	}
	_, _ = Inspect(context.Background(), api, dir)
	run, err := LoadRun(dir)
	if err != nil || run.Objects[0].RemoteID == "" {
		t.Fatal("root not reconciled", err)
	}
	if err = Cleanup(context.Background(), api, dir); err != nil || len(api.trash) != 1 {
		t.Fatal("root cleanup failed", err)
	}
}
func TestAmbiguousChildReconciliation(t *testing.T) {
	api, dir, run := seedFake(t)
	last := len(run.Objects) - 1
	run.Objects[last].RemoteID = ""
	run.Objects[last].Status = "pending"
	if err := saveRun(dir, run); err != nil {
		t.Fatal(err)
	}
	if _, err := Inspect(context.Background(), api, dir); err != nil {
		t.Fatal(err)
	}
	got, err := LoadRun(dir)
	if err != nil || got.Objects[last].RemoteID == "" {
		t.Fatal("child not reconciled")
	}
}
func TestInspectRemoteHashes(t *testing.T) {
	api, dir, _ := seedFake(t)
	report, err := Inspect(context.Background(), api, dir)
	if err != nil || report.Files != 5 {
		t.Fatal("inspection failed", err)
	}
	for id := range api.content {
		api.content[id] = []byte("corrupt")
		break
	}
	if _, err := Inspect(context.Background(), api, dir); err == nil {
		t.Fatal("inspection accepted corrupt content")
	}
}
func TestCleanupOwnedOnly(t *testing.T) {
	api, dir, run := seedFake(t)
	v := api.files[run.Objects[1].RemoteID]
	v.Properties = map[string]string{}
	api.files[v.ID] = v
	if err := Cleanup(context.Background(), api, dir); err == nil || len(api.trash) != 0 {
		t.Fatal("cleanup accepted unowned object")
	}
}
func TestCleanupIncompleteScan(t *testing.T) {
	api, dir, _ := seedFake(t)
	api.failList = true
	if err := Cleanup(context.Background(), api, dir); err == nil || len(api.trash) != 0 {
		t.Fatal("cleanup used incomplete listing")
	}
}
func TestCleanupUnknownDescendant(t *testing.T) {
	api, dir, run := seedFake(t)
	api.files["unknown"] = drive.File{ID: "unknown", Parents: []string{run.Objects[0].RemoteID}}
	if err := Cleanup(context.Background(), api, dir); err == nil || len(api.trash) != 0 {
		t.Fatal("cleanup trashed unknown descendant")
	}
}
func TestCleanupResumes(t *testing.T) {
	api, dir, _ := seedFake(t)
	if err := Cleanup(context.Background(), api, dir); err != nil {
		t.Fatal(err)
	}
	count := len(api.trash)
	if err := Cleanup(context.Background(), api, dir); err != nil || len(api.trash) != count {
		t.Fatal("cleanup not idempotent", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "run.json"))
	if err != nil || bytes.Contains(data, []byte("access_token")) {
		t.Fatal("invalid run record")
	}
}
