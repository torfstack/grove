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
	files                   map[string]drive.File
	data                    map[string]string
	downloads               int
	mutations               int
	incomplete              bool
	failDownloadID          string
	failTrashVerificationID string
}

func (r *remoteHTTP) wire(f drive.File) map[string]any {
	return map[string]any{"id": f.ID, "name": f.Name, "mimeType": f.MIMEType, "version": f.Version, "modifiedTime": f.ModifiedTime, "headRevisionId": f.HeadRevisionID, "parents": f.Parents, "size": strconv.FormatInt(f.Size, 10), "md5Checksum": f.MD5, "ownedByMe": f.OwnedByMe, "trashed": f.Trashed, "explicitlyTrashed": f.ExplicitlyTrashed, "capabilities": map[string]bool{"canDownload": f.CanDownload}}
}
func (r *remoteHTTP) serve(w http.ResponseWriter, q *http.Request) {
	if q.Method == http.MethodPatch || q.Method == http.MethodPost || q.Method == http.MethodDelete {
		r.mutations++
	}
	id := strings.TrimPrefix(q.URL.Path, "/drive/v3/files/")
	if q.URL.Path == "/drive/v3/files" {
		items := []map[string]any{}
		for _, f := range r.files {
			if !f.Trashed && len(f.Parents) == 1 && strings.Contains(q.URL.Query().Get("q"), "'"+f.Parents[0]+"' in parents") {
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
	if q.Method == http.MethodGet && f.Trashed && id == r.failTrashVerificationID {
		http.Error(w, "injected verification read failure", http.StatusServiceUnavailable)
		return
	}
	if q.Method == http.MethodPatch {
		var update struct {
			Trashed bool `json:"trashed"`
		}
		if err := json.NewDecoder(q.Body).Decode(&update); err != nil || !update.Trashed {
			http.Error(w, "invalid trash request", http.StatusBadRequest)
			return
		}
		f.Trashed = true
		f.ExplicitlyTrashed = true
		r.files[id] = f
		trashedParents := map[string]bool{id: true}
		for changed := true; changed; {
			changed = false
			for childID, child := range r.files {
				if child.Trashed || len(child.Parents) != 1 || !trashedParents[child.Parents[0]] {
					continue
				}
				child.Trashed = true
				r.files[childID] = child
				trashedParents[childID] = true
				changed = true
			}
		}
	}
	if q.URL.Query().Get("alt") == "media" {
		r.downloads++
		if id == r.failDownloadID {
			http.Error(w, "injected download failure", http.StatusInternalServerError)
			return
		}
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
	r.files["folder"] = drive.File{ID: "folder", Name: "old", MIMEType: drive.FolderMIME, Version: "1", ModifiedTime: "2026-01-01T00:00:00Z", OwnedByMe: true, Parents: []string{"root"}}
	r.put("a", "a", "folder", "old")
	r.put("b", "b", "root", "same")
	server := httptest.NewServer(http.HandlerFunc(r.serve))
	t.Cleanup(server.Close)
	service := Service{RegistryDir: registry, OpenAPI: func(context.Context, string, string) (drive.API, func() error, error) {
		return drive.NewClient(server.Client(), drive.ClientOptions{BaseURL: server.URL + "/drive/v3"}), func() error { return nil }, nil
	}}
	return service, opts, r
}

func advanceMetadataOnDownload(t *testing.T, service Service, remote *remoteHTTP, id string) Service {
	t.Helper()
	advanced := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !advanced && r.Method == http.MethodGet && r.URL.Path == "/drive/v3/files/"+id && r.URL.Query().Get("alt") == "media" {
			f := remote.files[id]
			f.Version = "10"
			remote.files[id] = f
			advanced = true
		}
		remote.serve(w, r)
	}))
	t.Cleanup(server.Close)
	service.OpenAPI = func(context.Context, string, string) (drive.API, func() error, error) {
		return drive.NewClient(server.Client(), drive.ClientOptions{BaseURL: server.URL + "/drive/v3"}), func() error { return nil }, nil
	}
	return service
}

func testProfileState(t *testing.T, opts Options) State {
	t.Helper()
	canonical, err := canonicalOptions(opts)
	if err != nil {
		t.Fatal(err)
	}
	state, err := loadState(canonical.ProfileDir, Binding{RemoteRoot: canonical.RemoteRoot, LocalDir: canonical.LocalDir, TokenFile: canonical.TokenFile})
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func TestServiceDownloadMetadataVersionReconciliation(t *testing.T) {
	for _, replace := range []bool{false, true} {
		name := "initial"
		if replace {
			name = "replacement"
		}
		t.Run(name, func(t *testing.T) {
			service, opts, remote := httpService(t)
			want := "old"
			if replace {
				if _, err := service.Run(context.Background(), opts); err != nil {
					t.Fatal(err)
				}
				want = "replacement bytes"
				remote.put("a", "a", "folder", want)
			}
			f := remote.files["a"]
			f.Version = "9"
			remote.files["a"] = f
			service = advanceMetadataOnDownload(t, service, remote, "a")
			if _, err := service.Run(context.Background(), opts); err != nil {
				t.Fatal(err)
			}
			state := testProfileState(t, opts)
			found := false
			for _, c := range state.Completed {
				if c.RemoteID == "a" {
					found = true
					if c.Version != "9" {
						t.Fatal("planned snapshot not retained")
					}
				}
			}
			if !found || remote.files["a"].Version != "10" {
				t.Fatal("metadata advance not exercised")
			}
			path := filepath.Join(opts.LocalDir, "old", "a")
			before, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			downloads := remote.downloads
			result, err := service.Run(context.Background(), opts)
			if err != nil || result.Downloaded != 0 || remote.downloads != downloads || remote.mutations != 0 {
				t.Fatalf("metadata reconciliation transferred or mutated: %v", err)
			}
			state = testProfileState(t, opts)
			for _, c := range state.Completed {
				if c.RemoteID == "a" && c.Version != "10" {
					t.Fatal("fresh metadata not recorded")
				}
			}
			after, err := os.Stat(path)
			if err != nil || !before.ModTime().Equal(after.ModTime()) {
				t.Fatal("metadata reconciliation rewrote target")
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != want {
				t.Fatalf("wrong bytes after reconciliation: %v", err)
			}
		})
	}
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

func TestServiceUsesReadOnlyAccessByDefault(t *testing.T) {
	service, opts, _ := httpService(t)
	access := ""
	original := service.OpenAPI
	service.OpenAPI = func(ctx context.Context, token, mode string) (drive.API, func() error, error) {
		access = mode
		return original(ctx, token, mode)
	}
	if _, err := service.Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if access != "read-only" {
		t.Fatalf("download-only run requested %q access", access)
	}
}

func TestTwoWayRejectsNewProfileBeforeOpeningDrive(t *testing.T) {
	service, opts, _ := httpService(t)
	opened := false
	service.OpenAPI = func(context.Context, string, string) (drive.API, func() error, error) {
		opened = true
		return nil, nil, errors.New("unexpected Drive session")
	}
	opts.TwoWay = true
	if _, err := service.Run(context.Background(), opts); err == nil || opened {
		t.Fatalf("new profile entered two-way mode: opened=%v err=%v", opened, err)
	}
}

func TestTwoWayRequiresSyncMutationAPI(t *testing.T) {
	service, opts, _ := httpService(t)
	if _, err := service.Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	service.OpenAPI = func(context.Context, string, string) (drive.API, func() error, error) {
		return tree(), func() error { return nil }, nil
	}
	opts.TwoWay = true
	if _, err := service.Run(context.Background(), opts); err == nil {
		t.Fatal("two-way run accepted a read-only API")
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

func TestTwoWayIncompleteRemoteScanDoesNotStartDeletion(t *testing.T) {
	service, opts, remote := httpService(t)
	if _, err := service.Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(opts.LocalDir, "old", "a")); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(opts.ProfileDir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	remote.incomplete = true
	opts.TwoWay = true
	if _, err := service.Run(context.Background(), opts); err == nil {
		t.Fatal("two-way sync accepted an incomplete remote scan")
	}
	after, err := os.ReadFile(filepath.Join(opts.ProfileDir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) || remote.mutations != 0 {
		t.Fatalf("incomplete scan changed profile or attempted remote mutation: mutations=%d", remote.mutations)
	}
	if _, err := os.Stat(filepath.Join(opts.LocalDir, "old", "a")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("preflight recreated the deleted local file")
	}
}

func TestTwoWayLocalDeletionMovesRemoteItemToTrash(t *testing.T) {
	service, opts, remote := httpService(t)
	if _, err := service.Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(opts.LocalDir, "old", "a")); err != nil {
		t.Fatal(err)
	}
	opts.TwoWay = true
	result, err := service.Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if result.Trashed != 1 || !remote.files["a"].Trashed || !remote.files["a"].ExplicitlyTrashed {
		t.Fatalf("local deletion was not moved to Drive trash: result=%+v file=%+v", result, remote.files["a"])
	}
	stateData, err := os.ReadFile(filepath.Join(opts.ProfileDir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var state State
	if err = json.Unmarshal(stateData, &state); err != nil || hasCompletedPath(state.Completed, "old/a") {
		t.Fatalf("trashed item remains in the active baseline: %+v err=%v", state.Completed, err)
	}
}

func TestTwoWayLocalFolderDeletionTrashesOneSubtreeRoot(t *testing.T) {
	service, opts, remote := httpService(t)
	if _, err := service.Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(opts.LocalDir, "old")); err != nil {
		t.Fatal(err)
	}
	opts.TwoWay = true
	result, err := service.Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if result.Trashed != 2 || !remote.files["folder"].ExplicitlyTrashed || !remote.files["a"].Trashed || remote.files["a"].ExplicitlyTrashed {
		t.Fatalf("folder trash did not preserve inherited trash semantics: result=%+v folder=%+v child=%+v", result, remote.files["folder"], remote.files["a"])
	}
	if remote.mutations != 1 {
		t.Fatalf("folder subtree used %d remote mutations, want one", remote.mutations)
	}
	stateData, err := os.ReadFile(filepath.Join(opts.ProfileDir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var state State
	if err = json.Unmarshal(stateData, &state); err != nil || hasCompletedPath(state.Completed, "old") || hasCompletedPath(state.Completed, "old/a") {
		t.Fatalf("trashed subtree remains in active baseline: %+v err=%v", state.Completed, err)
	}
}

func TestTrashReadFailureRecoversWithoutRepeatingMutation(t *testing.T) {
	service, opts, remote := httpService(t)
	if _, err := service.Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(opts.LocalDir, "old")); err != nil {
		t.Fatal(err)
	}
	remote.failTrashVerificationID = "a"
	opts.TwoWay = true
	if _, err := service.Run(context.Background(), opts); err == nil {
		t.Fatal("verification failure ignored")
	}
	if remote.mutations != 1 {
		t.Fatalf("trash mutation count: %d", remote.mutations)
	}
	remote.failTrashVerificationID = ""
	opts.TwoWay = false
	result, err := service.Run(context.Background(), opts)
	if err != nil || result.Trashed != 2 || remote.mutations != 1 {
		t.Fatalf("trash recovery repeated mutation or lost count: %+v %v mutations=%d", result, err, remote.mutations)
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
	canonical, err := canonicalOptions(opts)
	if err != nil {
		t.Fatal(err)
	}
	binding := Binding{RemoteRoot: canonical.RemoteRoot, LocalDir: canonical.LocalDir, TokenFile: canonical.TokenFile}
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

func TestIncrementalCaseAliasPreflight(t *testing.T) {
	service, opts, r := httpService(t)
	if _, err := service.Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(opts.LocalDir, "B")); err != nil {
		t.Skip("case-sensitive filesystem")
	}
	f := r.files["b"]
	f.Name = "B"
	r.files["b"] = f
	r.files["added-folder"] = drive.File{ID: "added-folder", Name: "!added", MIMEType: drive.FolderMIME, OwnedByMe: true, Parents: []string{"root"}}
	if _, err := service.Run(context.Background(), opts); err == nil {
		t.Fatal("case alias accepted")
	}
	if _, err := os.Stat(filepath.Join(opts.LocalDir, "!added")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("new operation executed before collision preflight")
	}
}
