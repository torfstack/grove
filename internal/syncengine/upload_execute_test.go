package syncengine

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/torfstack/grove/internal/drive"
)

type uploadAPI struct {
	file                   drive.File
	data                   []byte
	revisions              []drive.Revision
	revisionData           map[string][]byte
	revisionDownloadErrs   map[string]error
	revisionDownloadCounts map[string]int
	extraRaceRevisions     []drive.Revision
	pinned                 map[string]bool
	updates                int
	race                   bool
	revisionDownloadErr    error
	pinErr                 error
	getErr                 error
}

func (a *uploadAPI) Get(_ context.Context, id string) (drive.File, error) {
	if id == "root" {
		return drive.File{ID: "root", Name: "root", MIMEType: drive.FolderMIME, OwnedByMe: true}, nil
	}
	if a.getErr != nil {
		return drive.File{}, a.getErr
	}
	return a.file, nil
}
func (a *uploadAPI) List(context.Context, string, string) (drive.Page, error) {
	return drive.Page{Files: []drive.File{a.file}}, nil
}
func (a *uploadAPI) Download(_ context.Context, _ string, w io.Writer) error {
	_, err := w.Write(a.data)
	return err
}
func (*uploadAPI) Create(context.Context, drive.Create, io.Reader) (drive.File, error) {
	return drive.File{}, nil
}
func (*uploadAPI) Trash(context.Context, string) error                { return nil }
func (*uploadAPI) GenerateIDs(context.Context, int) ([]string, error) { return nil, nil }
func (*uploadAPI) CreateWithID(context.Context, string, drive.Create, io.Reader, int64) (drive.File, error) {
	return drive.File{}, nil
}
func (a *uploadAPI) UpdateContent(_ context.Context, id string, _ drive.Update, content io.Reader, size int64) (drive.File, error) {
	data, err := io.ReadAll(content)
	if err != nil || int64(len(data)) != size {
		return drive.File{}, err
	}
	a.data = data
	a.updates++
	version, _ := strconv.Atoi(a.file.Version)
	version++
	a.file.Version = strconv.Itoa(version)
	a.file.MD5 = md5Hex(data)
	a.file.Size = int64(len(data))
	a.file.HeadRevisionID = "rev-" + strconv.Itoa(version)
	a.file.ModifiedTime = "2026-01-0" + strconv.Itoa(version) + "T00:00:00Z"
	a.revisions = append(a.revisions, drive.Revision{ID: a.file.HeadRevisionID, MD5: a.file.MD5, ModifiedTime: a.file.ModifiedTime, Size: a.file.Size, HasSize: true})
	if a.race {
		a.revisions = append(a.revisions, drive.Revision{ID: "rev-racer", MD5: md5Hex([]byte("competing bytes")), ModifiedTime: "2026-01-01T12:00:00Z", Size: int64(len("competing bytes")), HasSize: true})
	}
	a.revisions = append(a.revisions, a.extraRaceRevisions...)
	return a.file, nil
}
func (a *uploadAPI) ListRevisions(context.Context, string) ([]drive.Revision, error) {
	return append([]drive.Revision(nil), a.revisions...), nil
}
func (a *uploadAPI) SetRevisionKeepForever(_ context.Context, _, id string, keep bool) error {
	if !keep && !a.pinned[id] {
		return errors.New("revision is not pinned")
	}
	if keep && a.pinErr != nil {
		return a.pinErr
	}
	a.pinned[id] = keep
	for i := range a.revisions {
		if a.revisions[i].ID == id {
			a.revisions[i].KeepForever = keep
		}
	}
	return nil
}
func (a *uploadAPI) DownloadRevision(_ context.Context, _, revisionID string, w io.Writer) error {
	if a.revisionDownloadCounts != nil {
		a.revisionDownloadCounts[revisionID]++
	}
	if err := a.revisionDownloadErrs[revisionID]; err != nil {
		return err
	}
	if a.revisionDownloadErr != nil {
		return a.revisionDownloadErr
	}
	_, err := w.Write(a.revisionData[revisionID])
	return err
}

func md5Hex(data []byte) string {
	sum := md5.Sum(data)
	return hex.EncodeToString(sum[:])
}

func TestCurrentUploadRemoteAllowsVersionOnlyMetadataChange(t *testing.T) {
	remote := file("a", "a")
	remote.Size, remote.MD5 = 3, md5Hex([]byte("old"))
	entry := Entry{Path: "a", Remote: remote}
	current := remote
	current.Version = "2"
	ex := executor{api: &uploadAPI{file: current}}
	if err := ex.currentUploadRemote(context.Background(), entry); err != nil {
		t.Fatalf("version-only metadata increase invalidated unchanged revision: %v", err)
	}
	current.HeadRevisionID = "rev-changed"
	ex.api = &uploadAPI{file: current}
	if err := ex.currentUploadRemote(context.Background(), entry); err == nil {
		t.Fatal("changed content revision passed current-remote validation")
	}
}

func TestUploadUpdatesWhenRemoteMatchesSavedRevision(t *testing.T) {
	localDir, profileDir := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(localDir, "a"), []byte("local edit"), 0600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(localDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	remote := file("a", "a")
	remote.HeadRevisionID = "rev-1"
	remote.Size, remote.MD5 = 3, md5Hex([]byte("old"))
	api := &uploadAPI{file: remote, data: []byte("old"), revisions: []drive.Revision{{ID: "rev-1", MD5: md5Hex([]byte("old")), ModifiedTime: remote.ModifiedTime, Size: 3, HasSize: true}}, pinned: map[string]bool{}}
	before := baselineFile("a", "a", []byte("old"))
	before.Version, before.ModifiedTime, before.HeadRevisionID = remote.Version, remote.ModifiedTime, remote.HeadRevisionID
	state := State{Version: 3, Completed: []Completed{before}}
	save := func() error { return saveState(profileDir, state) }
	if err = save(); err != nil {
		t.Fatal(err)
	}
	op := Operation{Kind: OpUpload, Entry: Entry{Path: "a", Remote: remote}, Local: LocalEntry{Path: "a", Kind: "file", SHA256: baselineFile("", "", []byte("local edit")).SHA256, Size: int64(len("local edit"))}, Before: []Completed{before}}
	ex := executor{root: root, profileDir: profileDir, api: api, mutation: api, state: &state, save: save}
	if err = ex.execute(context.Background(), op); err != nil {
		t.Fatal(err)
	}
	if string(api.data) != "local edit" || state.Completed[0].MD5 != md5Hex(api.data) || state.Completed[0].HeadRevisionID != "rev-2" || state.Transaction != nil {
		t.Fatalf("upload did not advance the remote baseline: data=%q state=%+v", api.data, state)
	}
	if !api.pinned["rev-1"] {
		t.Fatal("verified preflight revision was not retained for Drive's irreversible keepForever state")
	}
}

func TestRecoverPreflightPinRejectionWithoutBlindUnpin(t *testing.T) {
	localDir, profileDir := t.TempDir(), t.TempDir()
	localBytes := []byte("local edit")
	if err := os.WriteFile(filepath.Join(localDir, "a"), localBytes, 0600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(localDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	remote := file("a", "a")
	remote.HeadRevisionID = "rev-1"
	remote.Size, remote.MD5 = 3, md5Hex([]byte("old"))
	api := &uploadAPI{file: remote, data: []byte("old"), revisions: []drive.Revision{{ID: "rev-1", MD5: remote.MD5, ModifiedTime: remote.ModifiedTime, Size: 3, HasSize: true}}, pinned: map[string]bool{}, pinErr: errors.New("pin quota exhausted")}
	baseline := baselineFile("a", "a", []byte("old"))
	baseline.ModifiedTime, baseline.HeadRevisionID = remote.ModifiedTime, remote.HeadRevisionID
	state := State{Version: 3, Completed: []Completed{baseline}}
	save := func() error { return saveState(profileDir, state) }
	if err = save(); err != nil {
		t.Fatal(err)
	}
	op := Operation{Kind: OpUpload, Entry: Entry{Path: "a", Remote: remote}, Local: LocalEntry{Path: "a", Kind: "file", SHA256: hashText(string(localBytes)), Size: int64(len(localBytes))}, Before: []Completed{baseline}}
	ex := executor{root: root, profileDir: profileDir, api: api, mutation: api, state: &state, save: save, resumeUploads: true}
	if err = ex.execute(context.Background(), op); err == nil {
		t.Fatal("upload continued after preflight revision pin failure")
	}
	if state.Transaction == nil || state.Transaction.Phase != "pin-intent" || api.updates != 0 {
		t.Fatalf("pin rejection did not stop before upload: state=%+v updates=%d", state, api.updates)
	}
	api.pinErr = nil
	if err = ex.recoverUpload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if state.Transaction != nil || api.updates != 0 || api.pinned["rev-1"] {
		t.Fatalf("recovery did not safely clear a rejected pin intent: state=%+v updates=%d pins=%+v", state, api.updates, api.pinned)
	}
}

func TestRecoverPreflightPinPreservesPriorOwnership(t *testing.T) {
	for _, phase := range []string{"pin-intent", "pinned"} {
		for _, ownership := range []string{"existing", "grove", "unknown"} {
			t.Run(phase+"/"+ownership, func(t *testing.T) {
				localDir, profileDir := t.TempDir(), t.TempDir()
				if err := os.WriteFile(filepath.Join(localDir, "a"), []byte("edit"), 0600); err != nil {
					t.Fatal(err)
				}
				root, err := os.OpenRoot(localDir)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = root.Close() }()
				remote := file("a", "a")
				remote.Size, remote.MD5 = 3, md5Hex([]byte("old"))
				baseline := baselineFile("a", "a", []byte("old"))
				baseline.ModifiedTime, baseline.HeadRevisionID = remote.ModifiedTime, remote.HeadRevisionID
				current := remote
				current.Version = "2"
				api := &uploadAPI{file: current, revisions: []drive.Revision{{ID: remote.HeadRevisionID, KeepForever: true}}, pinned: map[string]bool{remote.HeadRevisionID: true}}
				state := State{Version: 3, Completed: []Completed{baseline}}
				ex := executor{root: root, profileDir: profileDir, api: api, mutation: api, state: &state, save: func() error { return saveState(profileDir, state) }, resumeUploads: true}
				op := Operation{Kind: OpUpload, Entry: Entry{Path: "a", Remote: remote}, Local: LocalEntry{Path: "a", Kind: "file", SHA256: hashText("edit"), Size: 4}, Before: []Completed{baseline}}
				if err = ex.prepareUpload(context.Background(), op); err != nil {
					t.Fatal(err)
				}
				j := *state.Transaction
				j.Phase = phase
				if phase == "pinned" {
					j.PinnedRevisionID = remote.HeadRevisionID
				}
				if ownership != "unknown" {
					wasKept := ownership == "existing"
					j.PreflightPinWasKept = &wasKept
				}
				if err = ex.journal(j); err != nil {
					t.Fatal(err)
				}
				if err = ex.recoverUpload(context.Background()); err != nil {
					t.Fatal(err)
				}
				if state.Transaction != nil || api.pinned[remote.HeadRevisionID] != (ownership != "grove") {
					t.Fatalf("recovery changed a pin it did not own: transaction=%v pins=%v", state.Transaction, api.pinned)
				}
				if _, err = os.Stat(filepath.Join(profileDir, j.StagePath)); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("abandoned pre-upload stage remains: %v", err)
				}
			})
		}
	}
}

func TestRecoverUncertainWritePreservesIntentUntilResultIsKnown(t *testing.T) {
	for _, kind := range []OperationKind{OpUpload, OpCreate} {
		t.Run(string(kind), func(t *testing.T) {
			localDir, profileDir := t.TempDir(), t.TempDir()
			if err := os.WriteFile(filepath.Join(localDir, "a"), []byte("edit"), 0600); err != nil {
				t.Fatal(err)
			}
			root, err := os.OpenRoot(localDir)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = root.Close() }()
			remote := file("a", "a")
			remote.Size, remote.MD5 = 3, md5Hex([]byte("old"))
			baseline := baselineFile("a", "a", []byte("old"))
			baseline.ModifiedTime, baseline.HeadRevisionID = remote.ModifiedTime, remote.HeadRevisionID
			api := &uploadAPI{file: remote, pinned: map[string]bool{remote.HeadRevisionID: true}}
			state := State{Version: 3}
			op := Operation{Kind: kind, Entry: Entry{Path: "a", Remote: remote}, Local: LocalEntry{Path: "a", Kind: "file", SHA256: hashText("edit"), Size: 4}}
			phase := "upload-intent"
			if kind == OpUpload {
				state.Completed, op.Before = []Completed{baseline}, []Completed{baseline}
			} else {
				op.OperationMarker = "create-marker"
				api.getErr = drive.ErrNotFound
				phase = "create-intent"
			}
			ex := executor{root: root, profileDir: profileDir, api: api, mutation: api, state: &state, save: func() error { return saveState(profileDir, state) }, resumeUploads: true}
			if err = ex.prepareUpload(context.Background(), op); err != nil {
				t.Fatal(err)
			}
			j := *state.Transaction
			j.Phase = phase
			if kind == OpUpload {
				j.PinnedRevisionID = remote.HeadRevisionID
			}
			if err = ex.journal(j); err != nil {
				t.Fatal(err)
			}
			if err = ex.recoverUpload(context.Background()); !errors.Is(err, errUploadPending) {
				t.Fatalf("uncertain operation was abandoned: %v", err)
			}
			if state.Transaction == nil || state.Transaction.Phase != phase || api.updates != 0 {
				t.Fatal("recovery discarded or repeated a potentially in-flight write")
			}
			if err = verifyPrivateStage(profileDir, j.StagePath, j.StageIdentity, j.StageSHA256, j.StageMD5, j.StageSize); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestUploadCapturesPostPreflightRevisionAsConflict(t *testing.T) {
	localDir, profileDir := t.TempDir(), t.TempDir()
	localBytes := []byte("local edit")
	if err := os.WriteFile(filepath.Join(localDir, "a"), localBytes, 0600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(localDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	remote := file("a", "a")
	remote.HeadRevisionID = "rev-1"
	remote.Size, remote.MD5 = 3, md5Hex([]byte("old"))
	api := &uploadAPI{file: remote, data: []byte("old"), revisions: []drive.Revision{{ID: "rev-1", MD5: remote.MD5, ModifiedTime: remote.ModifiedTime, Size: 3, HasSize: true}}, revisionData: map[string][]byte{"rev-racer": []byte("competing bytes")}, pinned: map[string]bool{}, race: true}
	baseline := baselineFile("a", "a", []byte("old"))
	baseline.ModifiedTime, baseline.HeadRevisionID = remote.ModifiedTime, remote.HeadRevisionID
	state := State{Version: 3, Completed: []Completed{baseline}}
	save := func() error { return saveState(profileDir, state) }
	if err = save(); err != nil {
		t.Fatal(err)
	}
	op := Operation{Kind: OpUpload, Entry: Entry{Path: "a", Remote: remote}, Local: LocalEntry{Path: "a", Kind: "file", SHA256: hashText(string(localBytes)), Size: int64(len(localBytes))}, Before: []Completed{baseline}}
	ex := executor{root: root, profileDir: profileDir, api: api, mutation: api, state: &state, save: save}
	if err = ex.execute(context.Background(), op); err != nil {
		t.Fatal(err)
	}
	if state.Transaction != nil || len(state.Conflicts) != 1 || state.Completed[0].HeadRevisionID != "rev-2" || !api.pinned["rev-racer"] || !api.pinned["rev-1"] {
		t.Fatalf("racing revision was not captured as a conflict: state=%+v pins=%+v", state, api.pinned)
	}
	conflict := state.Conflicts[0]
	if conflict.CloudRevisionID != "rev-racer" || conflict.CloudMD5 != md5Hex([]byte("competing bytes")) || conflict.CloudSize != int64(len("competing bytes")) {
		t.Fatalf("competing revision metadata was not recorded: %+v", conflict)
	}
	if err := verifyPrivateStage(profileDir, conflict.LocalStagePath, conflict.LocalStageIdentity, conflict.LocalSHA256, "", conflict.LocalSize); err != nil {
		t.Fatalf("local candidate was not retained: %v", err)
	}
	if err := verifyPrivateStage(profileDir, conflict.CloudStagePath, conflict.CloudStageIdentity, conflict.CloudSHA256, "", conflict.CloudSize); err != nil {
		t.Fatalf("cloud candidate was not retained: %v", err)
	}
	cloudStage, err := os.ReadFile(filepath.Join(profileDir, conflict.CloudStagePath))
	if err != nil || string(cloudStage) != "competing bytes" {
		t.Fatalf("cloud stage did not contain the racing revision: %q, %v", cloudStage, err)
	}
	loaded, err := loadState(profileDir, state.Binding)
	if err != nil || len(loaded.Conflicts) != 1 {
		t.Fatalf("captured conflict was not durable and valid: %+v, %v", loaded, err)
	}
}

func TestUploadCapturesAllPostPreflightRevisionsAsConflict(t *testing.T) {
	localDir, profileDir := t.TempDir(), t.TempDir()
	localBytes := []byte("local edit")
	if err := os.WriteFile(filepath.Join(localDir, "a"), localBytes, 0600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(localDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	remote := file("a", "a")
	remote.HeadRevisionID = "rev-1"
	remote.Size, remote.MD5 = 3, md5Hex([]byte("old"))
	first, second := []byte("candidate one"), []byte("candidate two")
	api := &uploadAPI{
		file: remote, data: []byte("old"), pinned: map[string]bool{},
		revisions:    []drive.Revision{{ID: "rev-1", MD5: remote.MD5, ModifiedTime: remote.ModifiedTime, Size: 3, HasSize: true}},
		revisionData: map[string][]byte{"rev-c": second, "rev-b": first},
		extraRaceRevisions: []drive.Revision{
			{ID: "rev-c", MD5: md5Hex(second), ModifiedTime: "2026-01-01T13:00:00Z", Size: int64(len(second)), HasSize: true},
			{ID: "rev-b", MD5: md5Hex(first), ModifiedTime: "2026-01-01T12:00:00Z", Size: int64(len(first)), HasSize: true},
		},
	}
	baseline := baselineFile("a", "a", []byte("old"))
	baseline.ModifiedTime, baseline.HeadRevisionID = remote.ModifiedTime, remote.HeadRevisionID
	state := State{Version: 3, Completed: []Completed{baseline}}
	save := func() error { return saveState(profileDir, state) }
	if err = save(); err != nil {
		t.Fatal(err)
	}
	op := Operation{Kind: OpUpload, Entry: Entry{Path: "a", Remote: remote}, Local: LocalEntry{Path: "a", Kind: "file", SHA256: hashText(string(localBytes)), Size: int64(len(localBytes))}, Before: []Completed{baseline}}
	ex := executor{root: root, profileDir: profileDir, api: api, mutation: api, state: &state, save: save}
	if err = ex.execute(context.Background(), op); err != nil {
		t.Fatal(err)
	}
	if state.Transaction != nil || len(state.Conflicts) != 1 {
		t.Fatalf("complete competitor set did not become a conflict: transaction=%+v conflicts=%+v", state.Transaction, state.Conflicts)
	}
	conflict := state.Conflicts[0]
	if conflict.CandidateSetVersion != 1 || len(conflict.CloudCandidates) != 2 {
		t.Fatalf("captured cloud candidate set = %+v", conflict.CloudCandidates)
	}
	if conflict.CloudCandidates[0].RevisionID != "rev-b" || conflict.CloudCandidates[1].RevisionID != "rev-c" {
		t.Fatalf("candidate ordering is unstable: %+v", conflict.CloudCandidates)
	}
	for i, candidate := range conflict.CloudCandidates {
		want := first
		if i == 1 {
			want = second
		}
		if !api.pinned[candidate.RevisionID] {
			t.Fatalf("candidate %s was not pinned", candidate.RevisionID)
		}
		if err = verifyPrivateStage(profileDir, candidate.StagePath, candidate.StageIdentity, hashText(string(want)), md5Hex(want), int64(len(want))); err != nil {
			t.Fatalf("candidate %s stage was not verified: %v", candidate.RevisionID, err)
		}
		contents, readErr := os.ReadFile(filepath.Join(profileDir, candidate.StagePath))
		if readErr != nil || string(contents) != string(want) {
			t.Fatalf("candidate %s stage = %q, %v", candidate.RevisionID, contents, readErr)
		}
	}
	if _, err = loadState(profileDir, state.Binding); err != nil {
		t.Fatalf("multi-candidate conflict was not durable: %v", err)
	}
}

func TestRecoverResumesMultipleCandidateCaptureAfterDownloadFailure(t *testing.T) {
	localDir, profileDir := t.TempDir(), t.TempDir()
	localBytes := []byte("local edit")
	if err := os.WriteFile(filepath.Join(localDir, "a"), localBytes, 0600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(localDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	remote := file("a", "a")
	remote.HeadRevisionID = "rev-1"
	remote.Size, remote.MD5 = 3, md5Hex([]byte("old"))
	first, second := []byte("candidate one"), []byte("candidate two")
	sentinel := errors.New("second candidate interrupted")
	api := &uploadAPI{
		file: remote, data: []byte("old"), pinned: map[string]bool{},
		revisions:            []drive.Revision{{ID: "rev-1", MD5: remote.MD5, ModifiedTime: remote.ModifiedTime, Size: 3, HasSize: true}},
		revisionData:         map[string][]byte{"rev-b": first, "rev-c": second},
		revisionDownloadErrs: map[string]error{"rev-c": sentinel}, revisionDownloadCounts: map[string]int{},
		extraRaceRevisions: []drive.Revision{
			{ID: "rev-b", MD5: md5Hex(first), ModifiedTime: "2026-01-01T12:00:00Z", Size: int64(len(first)), HasSize: true},
			{ID: "rev-c", MD5: md5Hex(second), ModifiedTime: "2026-01-01T13:00:00Z", Size: int64(len(second)), HasSize: true},
		},
	}
	baseline := baselineFile("a", "a", []byte("old"))
	baseline.ModifiedTime, baseline.HeadRevisionID = remote.ModifiedTime, remote.HeadRevisionID
	state := State{Version: 3, Completed: []Completed{baseline}}
	save := func() error { return saveState(profileDir, state) }
	if err = save(); err != nil {
		t.Fatal(err)
	}
	op := Operation{Kind: OpUpload, Entry: Entry{Path: "a", Remote: remote}, Local: LocalEntry{Path: "a", Kind: "file", SHA256: hashText(string(localBytes)), Size: int64(len(localBytes))}, Before: []Completed{baseline}}
	ex := executor{root: root, profileDir: profileDir, api: api, mutation: api, state: &state, save: save, resumeUploads: true}
	if err = ex.execute(context.Background(), op); !errors.Is(err, sentinel) {
		t.Fatalf("capture did not stop at the injected second-candidate failure: %v", err)
	}
	if state.Transaction == nil || !api.pinned["rev-b"] || !api.pinned["rev-c"] || len(state.Conflicts) != 0 {
		t.Fatalf("partial capture discarded obligations or published early: transaction=%+v conflicts=%+v pins=%+v", state.Transaction, state.Conflicts, api.pinned)
	}
	api.revisionDownloadErrs = nil
	if err = ex.recoverUpload(context.Background()); err != nil {
		t.Fatalf("retry did not resume the captured candidate set: %v", err)
	}
	if state.Transaction != nil || len(state.Conflicts) != 1 || len(state.Conflicts[0].CloudCandidates) != 2 {
		t.Fatalf("retry did not publish the complete candidate set: transaction=%+v conflicts=%+v", state.Transaction, state.Conflicts)
	}
	if api.revisionDownloadCounts["rev-b"] != 1 || api.revisionDownloadCounts["rev-c"] != 2 || api.updates != 1 {
		t.Fatalf("retry repeated completed work: downloads=%v updates=%d", api.revisionDownloadCounts, api.updates)
	}
}

func TestRecoverRetriesIncompleteCompetingRevisionCapture(t *testing.T) {
	localDir, profileDir := t.TempDir(), t.TempDir()
	localBytes := []byte("local edit")
	if err := os.WriteFile(filepath.Join(localDir, "a"), localBytes, 0600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(localDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	remote := file("a", "a")
	remote.HeadRevisionID = "rev-1"
	remote.Size, remote.MD5 = 3, md5Hex([]byte("old"))
	api := &uploadAPI{file: remote, data: []byte("old"), revisions: []drive.Revision{{ID: "rev-1", MD5: remote.MD5, ModifiedTime: remote.ModifiedTime, Size: 3, HasSize: true}}, revisionData: map[string][]byte{"rev-racer": []byte("competing bytes")}, pinned: map[string]bool{}, race: true, revisionDownloadErr: errors.New("temporary revision read failure")}
	baseline := baselineFile("a", "a", []byte("old"))
	baseline.ModifiedTime, baseline.HeadRevisionID = remote.ModifiedTime, remote.HeadRevisionID
	state := State{Version: 3, Completed: []Completed{baseline}}
	save := func() error { return saveState(profileDir, state) }
	if err = save(); err != nil {
		t.Fatal(err)
	}
	op := Operation{Kind: OpUpload, Entry: Entry{Path: "a", Remote: remote}, Local: LocalEntry{Path: "a", Kind: "file", SHA256: hashText(string(localBytes)), Size: int64(len(localBytes))}, Before: []Completed{baseline}}
	ex := executor{root: root, profileDir: profileDir, api: api, mutation: api, state: &state, save: save, resumeUploads: true}
	if err = ex.execute(context.Background(), op); err == nil {
		t.Fatal("failed competing revision download was accepted")
	}
	if state.Transaction == nil || state.Transaction.Phase != "uploaded" || len(state.Transaction.CandidateCaptures) != 1 || state.Transaction.CandidateCaptures[0].Phase != "capture-intent" || !api.pinned["rev-racer"] {
		t.Fatalf("failed capture did not leave a recoverable journal: state=%+v pins=%+v", state, api.pinned)
	}
	if _, err = loadState(profileDir, state.Binding); err != nil {
		t.Fatalf("partial candidate capture journal was not restartable: %v", err)
	}
	api.revisionDownloadErr = nil
	if err = ex.recoverUpload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if state.Transaction != nil || len(state.Conflicts) != 1 || !api.pinned["rev-1"] {
		t.Fatalf("restart did not finish conflict capture: state=%+v pins=%+v", state, api.pinned)
	}
}

func TestInstallHistoricalCloudCandidateKeepsDriveHeadAsBaseline(t *testing.T) {
	localDir, profileDir := t.TempDir(), t.TempDir()
	localBytes, cloudBytes := []byte("local edit"), []byte("remote edit")
	if err := os.WriteFile(filepath.Join(localDir, "a"), localBytes, 0600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(localDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	cloudPath, cloudIdentity := createConflictTestStage(t, profileDir, ".grove-cloud-conflict-race", cloudBytes)
	baseline := baselineFile("a", "remote-a", localBytes)
	baseline.Version, baseline.ModifiedTime, baseline.HeadRevisionID = "3", "2026-01-03T00:00:00Z", "rev-uploaded"
	conflict := Conflict{
		Path: "a", Baseline: baseline, LocalSHA256: baseline.SHA256, LocalSize: baseline.Size,
		RemoteID: baseline.RemoteID, RemoteVersion: baseline.Version, RemoteMD5: baseline.MD5, RemoteSize: baseline.Size, RemoteRevisionID: baseline.HeadRevisionID,
		CloudRevisionID: "rev-racer", CloudMD5: md5Hex(cloudBytes), CloudSize: int64(len(cloudBytes)), CloudStagePath: cloudPath, CloudStageIdentity: &cloudIdentity, CloudSHA256: hashText(string(cloudBytes)),
	}
	conflict.ID = conflictID(conflict)
	state := State{Version: 3, Completed: []Completed{baseline}, Conflicts: []Conflict{conflict}}
	api := &uploadAPI{
		file:      drive.File{ID: baseline.RemoteID, Name: "a", Parents: []string{"root"}, OwnedByMe: true, Version: baseline.Version, ModifiedTime: baseline.ModifiedTime, HeadRevisionID: baseline.HeadRevisionID, Size: baseline.Size, MD5: baseline.MD5},
		data:      localBytes,
		revisions: []drive.Revision{{ID: baseline.HeadRevisionID, MD5: baseline.MD5, ModifiedTime: baseline.ModifiedTime, Size: baseline.Size, HasSize: true}, {ID: "rev-racer", MD5: md5Hex(cloudBytes), ModifiedTime: "2026-01-02T12:00:00Z", Size: int64(len(cloudBytes)), HasSize: true}},
		pinned:    map[string]bool{"rev-racer": true},
	}
	ex := executor{root: root, profileDir: profileDir, api: api, mutation: api, state: &state, save: func() error { return nil }}
	if err = ex.resolveConflictOperation(context.Background(), Operation{Kind: OpConflict, Entry: Entry{Path: "a", Remote: api.file}, Conflict: &conflict, ResolutionSide: SideCloud, Before: []Completed{baseline}}); err != nil {
		t.Fatal(err)
	}
	if state.Completed[0].SHA256 != hashText(string(cloudBytes)) || state.Completed[0].Size != int64(len(cloudBytes)) || string(api.data) != string(cloudBytes) {
		t.Fatalf("historical cloud choice was not uploaded as the new baseline: state=%+v remote=%q", state.Completed[0], api.data)
	}
	if !api.pinned["rev-racer"] {
		t.Fatal("resolved historical revision was unpinned despite Drive's irreversible keepForever state")
	}
}

func TestRecoverUploadIntentReconcilesMatchingRemoteWithoutRetry(t *testing.T) {
	localDir, profileDir := t.TempDir(), t.TempDir()
	localBytes := []byte("uploaded bytes")
	if err := os.WriteFile(filepath.Join(localDir, "a"), localBytes, 0600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(localDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	preflight := file("a", "a")
	preflight.HeadRevisionID = "rev-1"
	preflight.Size, preflight.MD5 = 3, md5Hex([]byte("old"))
	baseline := baselineFile("a", "a", []byte("old"))
	baseline.ModifiedTime, baseline.HeadRevisionID = preflight.ModifiedTime, preflight.HeadRevisionID
	current := preflight
	current.Version, current.HeadRevisionID, current.ModifiedTime = "2", "rev-2", "2026-01-02T00:00:00Z"
	current.Size, current.MD5 = int64(len(localBytes)), md5Hex(localBytes)
	api := &uploadAPI{file: current, data: localBytes, revisions: []drive.Revision{{ID: "rev-1", ModifiedTime: preflight.ModifiedTime}, {ID: "rev-2", ModifiedTime: current.ModifiedTime}}, pinned: map[string]bool{"rev-1": true}}
	state := State{Version: 3, Completed: []Completed{baseline}}
	save := func() error { return saveState(profileDir, state) }
	if err = save(); err != nil {
		t.Fatal(err)
	}
	localSHA := baselineFile("", "", localBytes).SHA256
	op := Operation{Kind: OpUpload, Entry: Entry{Path: "a", Remote: preflight}, Local: LocalEntry{Path: "a", Kind: "file", SHA256: localSHA, Size: int64(len(localBytes))}, Before: []Completed{baseline}}
	ex := executor{root: root, profileDir: profileDir, api: api, mutation: api, state: &state, save: save, resumeUploads: true}
	if err = ex.prepareUpload(context.Background(), op); err != nil {
		t.Fatal(err)
	}
	j := *state.Transaction
	j.Phase, j.PinnedRevisionID = "upload-intent", "rev-1"
	if err = ex.journal(j); err != nil {
		t.Fatal(err)
	}
	if err = ex.recoverUpload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if api.updates != 0 || state.Transaction != nil || state.Completed[0].HeadRevisionID != "rev-2" || state.Completed[0].SHA256 != localSHA || !api.pinned["rev-1"] {
		t.Fatalf("uncertain upload was not reconciled safely: updates=%d state=%+v pins=%+v", api.updates, state, api.pinned)
	}
}

func TestServiceUploadsLaterLocalEditsAgainstNewBaseline(t *testing.T) {
	localDir, profileDir, registryDir := filepath.Join(t.TempDir(), "local"), filepath.Join(t.TempDir(), "profile"), filepath.Join(t.TempDir(), "registry")
	for _, dir := range []string{localDir, profileDir} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(localDir, "a"), []byte("first local edit"), 0600); err != nil {
		t.Fatal(err)
	}
	token := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(token, []byte("unused"), 0600); err != nil {
		t.Fatal(err)
	}
	remote := file("a", "a")
	remote.HeadRevisionID = "rev-1"
	remote.Size, remote.MD5 = 3, md5Hex([]byte("old"))
	api := &uploadAPI{file: remote, data: []byte("old"), revisions: []drive.Revision{{ID: "rev-1", MD5: md5Hex([]byte("old")), ModifiedTime: remote.ModifiedTime, Size: 3, HasSize: true}}, pinned: map[string]bool{}}
	opts := Options{ProfileDir: profileDir, RemoteRoot: "root", LocalDir: localDir, TokenFile: token, TwoWay: true}
	canonical, err := canonicalOptions(opts)
	if err != nil {
		t.Fatal(err)
	}
	baseline := baselineFile("a", "a", []byte("old"))
	baseline.Version, baseline.ModifiedTime, baseline.HeadRevisionID = remote.Version, remote.ModifiedTime, remote.HeadRevisionID
	state := State{Version: 2, Binding: Binding{RemoteRoot: "root", LocalDir: canonical.LocalDir, TokenFile: canonical.TokenFile}, Completed: []Completed{baseline}, PopulationComplete: true}
	if err = saveState(profileDir, state); err != nil {
		t.Fatal(err)
	}
	service := Service{RegistryDir: registryDir, OpenAPI: func(_ context.Context, _, access string) (drive.API, func() error, error) {
		if access != "read-write" {
			t.Fatalf("two-way sync requested %q access", access)
		}
		return api, func() error { return nil }, nil
	}}
	root, err := os.OpenRoot(localDir)
	if err != nil {
		t.Fatal(err)
	}
	localSnapshot, err := scanLocal(context.Background(), root, nil)
	_ = root.Close()
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := Scan(context.Background(), api, "root")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildTwoWayPlan(snapshot, localSnapshot, state)
	if err != nil || len(plan.Operations) != 1 || plan.Operations[0].Kind != OpUpload {
		t.Fatalf("test did not produce an upload plan: plan=%+v err=%v local=%+v remote=%+v", plan, err, localSnapshot, snapshot)
	}
	firstResult, runErr := service.Run(context.Background(), opts)
	if runErr != nil {
		t.Fatal(runErr)
	}
	if string(api.data) != "first local edit" || api.file.HeadRevisionID != "rev-2" {
		t.Fatalf("first local edit did not upload: result=%+v data=%q remote=%+v", firstResult, api.data, api.file)
	}
	if err = os.WriteFile(filepath.Join(localDir, "a"), []byte("second local edit"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if string(api.data) != "second local edit" || api.file.HeadRevisionID != "rev-3" {
		t.Fatalf("later local edit did not upload: data=%q remote=%+v", api.data, api.file)
	}
}

func TestServiceRecordsAskConflictWithoutMutationAndKeepsItPending(t *testing.T) {
	localDir, profileDir, registryDir := filepath.Join(t.TempDir(), "local"), filepath.Join(t.TempDir(), "profile"), filepath.Join(t.TempDir(), "registry")
	for _, dir := range []string{localDir, profileDir} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(localDir, "a"), []byte("local candidate"), 0600); err != nil {
		t.Fatal(err)
	}
	token := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(token, []byte("unused"), 0600); err != nil {
		t.Fatal(err)
	}
	remote := file("a", "a")
	remote.Version, remote.HeadRevisionID, remote.ModifiedTime = "2", "rev-2", "2026-01-02T00:00:00Z"
	remote.Size, remote.MD5 = int64(len("cloud candidate")), md5Hex([]byte("cloud candidate"))
	api := &uploadAPI{file: remote, data: []byte("cloud candidate"), revisions: []drive.Revision{{ID: "rev-1"}, {ID: "rev-2"}}, pinned: map[string]bool{}}
	opts := Options{ProfileDir: profileDir, RemoteRoot: "root", LocalDir: localDir, TokenFile: token, TwoWay: true}
	canonical, err := canonicalOptions(opts)
	if err != nil {
		t.Fatal(err)
	}
	baseline := baselineFile("a", "a", []byte("old"))
	baseline.ModifiedTime, baseline.HeadRevisionID = "2026-01-01T00:00:00Z", "rev-1"
	state := State{Version: 2, Binding: Binding{RemoteRoot: "root", LocalDir: canonical.LocalDir, TokenFile: canonical.TokenFile}, Completed: []Completed{baseline}, PopulationComplete: true}
	if err = saveState(profileDir, state); err != nil {
		t.Fatal(err)
	}
	service := Service{RegistryDir: registryDir, OpenAPI: func(_ context.Context, _, access string) (drive.API, func() error, error) {
		if access != "read-write" && access != "read-only" {
			t.Fatalf("unexpected access mode %q", access)
		}
		return api, func() error { return nil }, nil
	}}
	if _, err = service.Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	conflicts, err := service.ListConflicts(context.Background(), opts)
	if err != nil || len(conflicts) != 1 {
		t.Fatalf("first run did not persist one conflict: %+v, %v", conflicts, err)
	}
	if api.updates != 0 || string(api.data) != "cloud candidate" {
		t.Fatal("ask policy mutated the remote candidate")
	}
	if _, err = service.Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	again, err := service.ListConflicts(context.Background(), opts)
	if err != nil || len(again) != 1 || again[0].ID != conflicts[0].ID {
		t.Fatalf("repeat run changed pending conflict identity: %+v, %v", again, err)
	}
	defaultOpts := opts
	defaultOpts.TwoWay = false
	if _, err = service.Run(context.Background(), defaultOpts); err == nil {
		t.Fatal("download-only run silently consumed a pending local conflict")
	}
	stillPending, err := service.ListConflicts(context.Background(), opts)
	if err != nil || len(stillPending) != 1 || api.updates != 0 {
		t.Fatalf("default restart mutated pending conflict: %+v, %v", stillPending, err)
	}
}
