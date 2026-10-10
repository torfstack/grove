package syncengine

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/torfstack/grove/internal/drive"
	"github.com/torfstack/grove/internal/privatefs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

type remoteHTTP struct {
	files      map[string]drive.File
	data       map[string]string
	downloads  int
	incomplete bool
}

func (r *remoteHTTP) wire(f drive.File) map[string]any {
	return map[string]any{"id": f.ID, "name": f.Name, "mimeType": f.MIMEType, "version": f.Version, "parents": f.Parents, "size": strconv.FormatInt(f.Size, 10), "md5Checksum": f.MD5, "ownedByMe": f.OwnedByMe, "capabilities": map[string]bool{"canDownload": f.CanDownload}}
}
func (r *remoteHTTP) serve(w http.ResponseWriter, q *http.Request) {
	id := strings.TrimPrefix(q.URL.Path, "/drive/v3/files/")
	if q.URL.Path == "/drive/v3/files" {
		items := []map[string]any{}
		for _, f := range r.files {
			if len(f.Parents) == 1 && strings.Contains(q.URL.Query().Get("q"), "'"+f.Parents[0]+"' in parents") {
				items = append(items, r.wire(f))
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"files": items, "incompleteSearch": r.incomplete})
		return
	}
	f, ok := r.files[id]
	if !ok {
		http.NotFound(w, q)
		return
	}
	if q.URL.Query().Get("alt") == "media" {
		r.downloads++
		_, _ = w.Write([]byte(r.data[id]))
		return
	}
	_ = json.NewEncoder(w).Encode(r.wire(f))
}
func (r *remoteHTTP) put(id, name, parent, data string) {
	f := file(id, name)
	f.Parents = []string{parent}
	f.Size = int64(len(data))
	sum := md5.Sum([]byte(data))
	f.MD5 = hex.EncodeToString(sum[:])
	r.files[id] = f
	r.data[id] = data
}
func httpService(t *testing.T) (Service, Options, *remoteHTTP) {
	t.Helper()
	opts, registry := options(t)
	r := &remoteHTTP{files: map[string]drive.File{}, data: map[string]string{}}
	r.files["root"] = drive.File{ID: "root", Name: "root", MIMEType: drive.FolderMIME, OwnedByMe: true}
	r.files["folder"] = drive.File{ID: "folder", Name: "old", MIMEType: drive.FolderMIME, OwnedByMe: true, Parents: []string{"root"}}
	r.put("a", "a", "folder", "old")
	r.put("b", "b", "root", "same")
	server := httptest.NewServer(http.HandlerFunc(r.serve))
	t.Cleanup(server.Close)
	service := Service{RegistryDir: registry, OpenAPI: func(context.Context, string) (drive.API, func() error, error) {
		return drive.NewClient(server.Client(), drive.ClientOptions{BaseURL: server.URL + "/drive/v3"}), func() error { return nil }, nil
	}}
	return service, opts, r
}
func TestServiceIncrementalHTTP(t *testing.T) {
	service, opts, r := httpService(t)
	ctx := context.Background()
	if _, err := service.Run(ctx, opts); err != nil {
		t.Fatal(err)
	}
	f := r.files["folder"]
	f.Name = "new"
	r.files["folder"] = f
	r.put("a", "renamed", "folder", "updated")
	f = r.files["a"]
	f.Version = "2"
	r.files["a"] = f
	r.put("c", "added", "root", "added")
	result, err := service.Run(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	if result.Downloaded != 2 || result.Updated != 1 || result.Moved != 2 {
		t.Fatalf("wrong counts %+v", result)
	}
	for path, want := range map[string]string{"new/renamed": "updated", "b": "same", "added": "added"} {
		b, err := os.ReadFile(filepath.Join(opts.LocalDir, path))
		if err != nil || string(b) != want {
			t.Fatalf("wrong %s", path)
		}
	}
	if _, err = os.Stat(filepath.Join(opts.LocalDir, "old")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("old folder retained")
	}
	before := r.downloads
	stateBefore, err := os.ReadFile(filepath.Join(opts.ProfileDir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	result, err = service.Run(ctx, opts)
	if err != nil || result.Downloaded != 0 || result.Moved != 0 || r.downloads != before {
		t.Fatal("no-op performed work", err)
	}
	stateAfter, _ := os.ReadFile(filepath.Join(opts.ProfileDir, "state.json"))
	if string(stateBefore) != string(stateAfter) {
		t.Fatal("no-op rewrote state")
	}
}
func TestServicePreflightPreservesDestination(t *testing.T) {
	for _, mode := range []string{"incomplete", "unsupported", "removal", "local-edit"} {
		t.Run(mode, func(t *testing.T) {
			service, opts, r := httpService(t)
			if _, err := service.Run(context.Background(), opts); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "incomplete":
				r.incomplete = true
			case "unsupported":
				f := r.files["a"]
				f.MIMEType = "application/vnd.google-apps.document"
				r.files["a"] = f
			case "removal":
				delete(r.files, "a")
			case "local-edit":
				if err := os.WriteFile(filepath.Join(opts.LocalDir, "old/a"), []byte("user"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			r.put("c", "added", "root", "added")
			before, _ := os.ReadFile(filepath.Join(opts.ProfileDir, "state.json"))
			calls := r.downloads
			if _, err := service.Run(context.Background(), opts); err == nil {
				t.Fatal("invalid preflight accepted")
			}
			after, _ := os.ReadFile(filepath.Join(opts.ProfileDir, "state.json"))
			if string(before) != string(after) || r.downloads != calls {
				t.Fatal("preflight mutated state or transferred")
			}
			if _, err := os.Stat(filepath.Join(opts.LocalDir, "added")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("addition created on failed preflight")
			}
		})
	}
}
func TestIncrementalWriterExclusion(t *testing.T) {
	service, opts, _ := httpService(t)
	if _, err := service.Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	lock, err := privatefs.Acquire(filepath.Join(opts.ProfileDir, "profile.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Close() }()
	if _, err = service.Run(context.Background(), opts); err == nil {
		t.Fatal("concurrent writer accepted")
	}
}

func TestServiceMigratesV1AndRecordsMetadata(t *testing.T) {
	service, opts, r := httpService(t)
	ctx := context.Background()
	if _, err := service.Run(ctx, opts); err != nil {
		t.Fatal(err)
	}
	binding := Binding{RemoteRoot: opts.RemoteRoot, LocalDir: opts.LocalDir, TokenFile: opts.TokenFile}
	s, err := loadState(opts.ProfileDir, binding)
	if err != nil {
		t.Fatal(err)
	}
	s.Version = 1
	if err = saveState(opts.ProfileDir, s); err != nil {
		t.Fatal(err)
	}
	f := r.files["b"]
	f.Version = "2"
	r.files["b"] = f
	calls := r.downloads
	result, err := service.Run(ctx, opts)
	if err != nil || result.Downloaded != 0 || r.downloads != calls {
		t.Fatal("metadata transferred", err)
	}
	s, err = loadState(opts.ProfileDir, binding)
	if err != nil || s.Version != 2 {
		t.Fatal("migration failed", err)
	}
	found := false
	for _, c := range s.Completed {
		if c.RemoteID == "b" {
			found = c.Version == "2"
		}
	}
	if !found {
		t.Fatal("metadata baseline not updated")
	}
}
