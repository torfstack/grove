package syncengine

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/torfstack/grove/internal/drive"
)

type recoveryAPI struct {
	*uploadAPI
	afterDownload func()
	listErr       error
}

func (a *recoveryAPI) Download(ctx context.Context, id string, w io.Writer) error {
	if err := a.uploadAPI.Download(ctx, id, w); err != nil {
		return err
	}
	if a.afterDownload != nil {
		a.afterDownload()
	}
	return nil
}
func (a *recoveryAPI) ListRevisions(ctx context.Context, id string) ([]drive.Revision, error) {
	if a.listErr != nil {
		return nil, a.listErr
	}
	return a.uploadAPI.ListRevisions(ctx, id)
}
func (a *recoveryAPI) GenerateIDs(context.Context, int) ([]string, error) {
	panic("recovery allocated replacement IDs")
}
func (a *recoveryAPI) CreateWithID(context.Context, string, drive.Create, io.Reader, int64) (drive.File, error) {
	panic("recovery created remote object")
}
func (a *recoveryAPI) UpdateContent(context.Context, string, drive.Update, io.Reader, int64) (drive.File, error) {
	panic("recovery wrote content")
}
func (a *recoveryAPI) List(context.Context, string, string) (drive.Page, error) {
	panic("recovery scanned tree")
}

func setRecoveredHead(t *testing.T, ex *executor, api *uploadAPI, kind OperationKind) {
	t.Helper()
	j := *ex.state.Transaction
	api.file.Size, api.file.MD5, api.data = 4, md5Hex([]byte("edit")), []byte("edit")
	api.file.Version, api.file.HeadRevisionID, api.file.ModifiedTime = "2", "rev-2", "2026-01-02T00:00:00Z"
	if kind == OpUpload {
		j.Phase, j.PinnedRevisionID = "upload-intent", j.PreflightRevisionID
		api.revisions = append(api.revisions, drive.Revision{ID: "rev-2", ModifiedTime: api.file.ModifiedTime, MD5: api.file.MD5, Size: 4, HasSize: true})
	} else {
		j.Phase = "create-intent"
		api.file.Properties = map[string]string{"grove_operation": j.OperationMarker}
	}
	if err := ex.journal(j); err != nil {
		t.Fatal(err)
	}
}

func TestRecoveredRemoteRequiresStructureAndStableBytes(t *testing.T) {
	for _, kind := range []OperationKind{OpUpload, OpCreate} {
		for _, mode := range []string{"valid", "id", "marker", "ownership", "trashed", "parent", "name", "type", "corrupt", "moving"} {
			if mode == "marker" && kind == OpUpload {
				continue
			}
			t.Run(string(kind)+"/"+mode, func(t *testing.T) {
				_, o, state, ex, base := recoveryFixture(t, kind)
				setRecoveredHead(t, ex, base, kind)
				api := &recoveryAPI{uploadAPI: base}
				ex.api, ex.mutation = api, api
				switch mode {
				case "id":
					base.file.ID = "alien"
				case "marker":
					base.file.Properties["grove_operation"] = "alien"
				case "ownership":
					base.file.OwnedByMe = false
				case "trashed":
					base.file.Trashed = true
				case "parent":
					base.file.Parents = []string{"elsewhere"}
				case "name":
					base.file.Name = "renamed"
				case "type":
					base.file.MIMEType = drive.FolderMIME
				case "corrupt":
					base.data = []byte("evil")
				case "moving":
					api.afterDownload = func() { base.file.HeadRevisionID = "rev-3" }
				}
				err := ex.recoverUpload(context.Background())
				if mode == "valid" {
					if err != nil || state.Transaction != nil || len(state.Completed) != 1 || state.Completed[0].SHA256 != hashText("edit") {
						t.Fatalf("valid recovery: %v %+v", err, state)
					}
					return
				}
				if err == nil || state.Transaction == nil {
					t.Fatalf("invalid outcome committed: %v", err)
				}
				if err = verifyPrivateStage(o.ProfileDir, state.Transaction.StagePath, state.Transaction.StageIdentity, state.Transaction.StageSHA256, state.Transaction.StageMD5, state.Transaction.StageSize); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestRecoveryRetainsUncertainIdentity(t *testing.T) {
	for _, kind := range []OperationKind{OpUpload, OpCreate} {
		t.Run(string(kind), func(t *testing.T) {
			_, _, state, ex, base := recoveryFixture(t, kind)
			j := *state.Transaction
			if kind == OpUpload {
				j.Phase, j.PinnedRevisionID = "upload-intent", j.PreflightRevisionID
			} else {
				j.Phase = "create-intent"
				base.getErr = drive.ErrNotFound
			}
			if err := ex.journal(j); err != nil {
				t.Fatal(err)
			}
			api := &recoveryAPI{uploadAPI: base}
			ex.api, ex.mutation = api, api
			err := ex.recoverUpload(context.Background())
			var blocked *RecoveryBlockedError
			if !errors.As(err, &blocked) || blocked.Reason != "uncertain-write" || state.Transaction == nil || state.Transaction.AllocatedID != j.AllocatedID {
				t.Fatalf("uncertain identity lost: %v", err)
			}
		})
	}
}

func TestRecoveryRechecksHistory(t *testing.T) {
	for _, mode := range []string{"complete", "missing-anchor", "invalid-time", "one", "multiple", "lost-competitor"} {
		t.Run(mode, func(t *testing.T) {
			_, _, state, ex, base := recoveryFixture(t, OpUpload)
			setRecoveredHead(t, ex, base, OpUpload)
			j := *state.Transaction
			j.RevisionHistoryUnverified = true
			if mode == "missing-anchor" {
				base.revisions = base.revisions[1:]
			}
			if mode == "invalid-time" {
				base.revisions = append(base.revisions, drive.Revision{ID: "bad", ModifiedTime: "invalid"})
			}
			if mode == "one" || mode == "multiple" {
				base.revisionData = map[string][]byte{"racer": []byte("cloud")}
				base.revisions = append(base.revisions, drive.Revision{ID: "racer", ModifiedTime: "2026-01-01T12:00:00Z", MD5: md5Hex([]byte("cloud")), Size: 5, HasSize: true})
			}
			if mode == "multiple" {
				base.revisions = append(base.revisions, drive.Revision{ID: "racer2", ModifiedTime: "2026-01-01T13:00:00Z", MD5: md5Hex([]byte("other")), Size: 5, HasSize: true})
			}
			if mode == "lost-competitor" {
				j.CompetingRevisionIDs = []string{"racer"}
			}
			if err := ex.journal(j); err != nil {
				t.Fatal(err)
			}
			err := ex.recoverUpload(context.Background())
			if mode == "complete" || mode == "one" {
				if err != nil || state.Transaction != nil {
					t.Fatalf("fresh evidence blocked: %v", err)
				}
				if mode == "one" && (len(state.Conflicts) != 1 || !base.pinned["racer"]) {
					t.Fatal("competitor not preserved")
				}
				return
			}
			if err == nil || state.Transaction == nil {
				t.Fatal("incomplete history accepted")
			}
			if mode == "multiple" && (!base.pinned["racer"] || !base.pinned["racer2"] || len(state.Transaction.CompetingRevisionIDs) != 2) {
				t.Fatal("competitors not preserved")
			}
			if mode == "lost-competitor" && len(state.Transaction.CompetingRevisionIDs) != 1 {
				t.Fatal("lost recorded competitor")
			}
		})
	}
}

func TestRecoveryRetiredCleanupResumes(t *testing.T) {
	for _, mode := range []string{"retire-save", "clear-save", "remove", "missing"} {
		t.Run(mode, func(t *testing.T) {
			_, o, state, ex, _ := recoveryFixture(t, OpUpload)
			j := *state.Transaction
			realSave := ex.save
			ex.save = func() error {
				if (mode == "retire-save" && state.Transaction != nil && state.Transaction.Phase == "retired") || (mode == "clear-save" && state.Transaction == nil) {
					return errors.New("save failure")
				}
				return realSave()
			}
			if mode == "remove" {
				old := j.StagePath + "-original"
				if err := os.Rename(filepath.Join(o.ProfileDir, j.StagePath), filepath.Join(o.ProfileDir, old)); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(o.ProfileDir, j.StagePath), []byte("edit"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "missing" {
				j.Phase = "retired"
				if err := ex.journal(j); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(filepath.Join(o.ProfileDir, j.StagePath)); err != nil {
					t.Fatal(err)
				}
			}
			err := ex.recoverUpload(context.Background())
			if mode == "missing" {
				if err != nil || state.Transaction != nil {
					t.Fatalf("retired cleanup didn't resume: %v", err)
				}
				return
			}
			if err == nil || state.Transaction == nil {
				t.Fatal("cleanup obligation lost")
			}
			if mode == "clear-save" {
				if state.Transaction.Phase != "retired" {
					t.Fatal("not retired")
				}
				loaded, err := loadState(o.ProfileDir, state.Binding)
				if err != nil || loaded.Transaction == nil {
					t.Fatalf("retired journal invalid: %v", err)
				}
				ex.save = realSave
				if err = ex.recoverUpload(context.Background()); err != nil || state.Transaction != nil {
					t.Fatalf("restart failed: %v", err)
				}
			}
			if mode == "retire-save" {
				if _, err := os.Stat(filepath.Join(o.ProfileDir, j.StagePath)); err != nil {
					t.Fatal("stage removed before retirement saved")
				}
			}
		})
	}
}

func TestRecoveredSavedOutcomesStillValidate(t *testing.T) {
	for _, kind := range []OperationKind{OpUpload, OpCreate} {
		for _, phase := range []string{"saved", "captured"} {
			if kind == OpCreate && phase == "captured" {
				continue
			}
			t.Run(string(kind)+"/"+phase, func(t *testing.T) {
				_, o, state, ex, api := recoveryFixture(t, kind)
				setRecoveredHead(t, ex, api, kind)
				j := *state.Transaction
				j.After = []Completed{baselineFile("a", "a", []byte("edit"))}
				j.After[0].Version, j.After[0].HeadRevisionID, j.After[0].ModifiedTime = api.file.Version, api.file.HeadRevisionID, api.file.ModifiedTime
				if kind == OpUpload {
					j.Phase = "uploaded"
				} else {
					j.Phase = "verified"
				}
				if phase == "captured" {
					j.Phase = "conflict-captured"
					path, id := createConflictTestStage(t, o.ProfileDir, ".grove-cloud-conflict-saved", []byte("cloud"))
					j.CloudStagePath, j.CloudStageIdentity, j.CloudSHA256, j.CloudMD5, j.CloudSize, j.CloudRevisionID = path, &id, hashText("cloud"), md5Hex([]byte("cloud")), 5, "racer"
					j.CompetingRevisionIDs = []string{"racer"}
				}
				if err := ex.journal(j); err != nil {
					t.Fatal(err)
				}
				api.file.Name = "moved"
				if err := ex.recoverUpload(context.Background()); err == nil || state.Transaction == nil || len(state.Conflicts) != 0 {
					t.Fatal("saved outcome bypassed remote revalidation")
				}
			})
		}
	}
}

func TestRecoveredFolderCreateValidatesWithoutMedia(t *testing.T) {
	_, _, state, ex, base := recoveryFixture(t, OpCreate)
	j := *state.Transaction
	if err := os.Remove(filepath.Join(ex.profileDir, j.StagePath)); err != nil {
		t.Fatal(err)
	}
	j.Operation.Local.Kind = "folder"
	j.Operation.Local.Size = 0
	j.Operation.Local.SHA256 = ""
	j.Operation.Entry.Remote.MIMEType = drive.FolderMIME
	j.StagePath = ""
	j.StageIdentity = nil
	j.StageSHA256 = ""
	j.StageMD5 = ""
	j.StageSize = 0
	j.Phase = "create-intent"
	base.file = j.Operation.Entry.Remote
	base.file.Properties = map[string]string{"grove_operation": j.OperationMarker}
	api := &recoveryAPI{uploadAPI: base, afterDownload: func() { t.Fatal("folder media downloaded") }}
	ex.api, ex.mutation = api, api
	if err := ex.journal(j); err != nil {
		t.Fatal(err)
	}
	if err := ex.recoverUpload(context.Background()); err != nil || state.Transaction != nil || len(state.Completed) != 1 || state.Completed[0].Kind != "folder" {
		t.Fatalf("folder failed: %v", err)
	}
}

func TestRecoveryResultSaveBoundaries(t *testing.T) {
	for _, failPhase := range []string{"uploaded", "committed", "clear"} {
		t.Run(failPhase, func(t *testing.T) {
			_, o, state, ex, api := recoveryFixture(t, OpUpload)
			setRecoveredHead(t, ex, api, OpUpload)
			realSave := ex.save
			sentinel := errors.New("save fault")
			ex.save = func() error {
				if (state.Transaction == nil && failPhase == "clear") || (state.Transaction != nil && state.Transaction.Phase == failPhase) {
					return sentinel
				}
				return realSave()
			}
			if err := ex.recoverUpload(context.Background()); !errors.Is(err, sentinel) || state.Transaction == nil {
				t.Fatalf("failure lost its journal: %v", err)
			}
			loaded, err := loadState(o.ProfileDir, state.Binding)
			if err != nil || loaded.Transaction == nil {
				t.Fatalf("not restartable %v", err)
			}
			*state = loaded
			ex.save = realSave
			if err = ex.recoverUpload(context.Background()); err != nil || state.Transaction != nil || state.Completed[0].SHA256 != hashText("edit") {
				t.Fatalf("restart %v", err)
			}
		})
	}
}
