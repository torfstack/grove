package syncengine

import (
	"context"
	"errors"
	"github.com/torfstack/grove/internal/drive"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func reviewCompetitor(base *uploadAPI) {
	base.revisionData = map[string][]byte{"racer": []byte("cloud")}
	base.revisions = append(base.revisions, drive.Revision{ID: "racer", ModifiedTime: "2026-01-01T12:00:00Z", MD5: md5Hex([]byte("cloud")), Size: 5, HasSize: true})
}

func TestRecoveryCommittedConflictOutcome(t *testing.T) {
	s, o, state, ex, base := recoveryFixture(t, OpUpload)
	setRecoveredHead(t, ex, base, OpUpload)
	reviewCompetitor(base)
	save := ex.save
	ex.save = func() error {
		if state.Transaction == nil {
			return errors.New("interrupted clear")
		}
		return save()
	}
	if err := ex.recoverUpload(context.Background()); err == nil {
		t.Fatal("expected injected failure")
	}
	if state.Transaction.Phase != "committed" || len(state.Conflicts) != 1 {
		t.Fatal("setup")
	}
	s.OpenAPI = func(context.Context, string, string) (drive.API, func() error, error) {
		return base, func() error { return nil }, nil
	}
	r, err := s.RetryRecovery(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if r.Outcome != "conflict-pending" {
		t.Fatalf("reported %q although this transaction's unresolved conflict survives", r.Outcome)
	}
}

func TestRecoveryInvalidHistoryAnchor(t *testing.T) {
	for _, which := range []int{0, 1} {
		t.Run(string(rune('0'+which)), func(t *testing.T) {
			_, _, state, ex, base := recoveryFixture(t, OpUpload)
			setRecoveredHead(t, ex, base, OpUpload)
			base.revisions[which].ModifiedTime = "invalid"
			j := *state.Transaction
			j.RevisionHistoryUnverified = true
			if err := ex.journal(j); err != nil {
				t.Fatal(err)
			}
			if err := ex.recoverUpload(context.Background()); err == nil {
				t.Fatal("accepted invalid timestamp anchor and cleared transaction")
			}
		})
	}
}

type reviewListFailureAPI struct {
	*uploadAPI
	n        int
	sentinel error
}

func (a *reviewListFailureAPI) ListRevisions(ctx context.Context, id string) ([]drive.Revision, error) {
	a.n++
	if a.n == 2 {
		return nil, a.sentinel
	}
	return a.uploadAPI.ListRevisions(ctx, id)
}
func TestRecoveryCaptureProviderIdentity(t *testing.T) {
	_, _, _, ex, base := recoveryFixture(t, OpUpload)
	setRecoveredHead(t, ex, base, OpUpload)
	reviewCompetitor(base)
	sentinel := errors.New("provider failure")
	api := &reviewListFailureAPI{uploadAPI: base, sentinel: sentinel}
	ex.api, ex.mutation = api, api
	if err := ex.recoverUpload(context.Background()); !errors.Is(err, sentinel) {
		t.Fatalf("provider cause lost: %v", err)
	}
}

func TestRecoveryResolutionInterruptLoadable(t *testing.T) {
	for _, phase := range []string{"staged", "pin-intent", "pinned", "upload-intent", "uploaded", "committed"} {
		t.Run(phase, func(t *testing.T) {
			service, o, state, ex, base := recoveryFixture(t, OpUpload)
			op := state.Transaction.Operation
			state.Transaction = nil
			c := Conflict{Path: "a", Baseline: state.Completed[0], LocalSHA256: hashText("edit"), LocalSize: 4, RemoteID: base.file.ID, RemoteVersion: base.file.Version, RemoteMD5: base.file.MD5, RemoteSize: base.file.Size}
			op.Conflict = &c
			if err := ex.recordConflict(context.Background(), op); err != nil {
				t.Fatal(err)
			}
			c = state.Conflicts[0]
			c.SelectedSide = SideLocal
			state.Conflicts[0] = c
			if err := saveState(o.ProfileDir, *state); err != nil {
				t.Fatal(err)
			}
			if _, err := loadState(o.ProfileDir, state.Binding); err != nil {
				t.Fatalf("setup invalid: %v", err)
			}
			save := ex.save
			ex.save = func() error {
				if err := save(); err != nil {
					return err
				}
				if state.Transaction != nil && state.Transaction.Phase == phase {
					return errors.New("interruption after durable save")
				}
				return nil
			}
			err := ex.resolveConflictOperation(context.Background(), Operation{Kind: OpConflict, Entry: op.Entry, Local: op.Local, Conflict: &c, ResolutionSide: SideLocal, Before: op.Before})
			if err == nil {
				t.Fatal("expected interruption")
			}
			loaded, err := loadState(o.ProfileDir, state.Binding)
			if err != nil {
				t.Fatalf("durable interrupted %s upload cannot load: %v", phase, err)
			}
			*state = loaded
			ex.save = save
			api := &recoveryAPI{uploadAPI: base}
			service.OpenAPI = func(context.Context, string, string) (drive.API, func() error, error) {
				return api, func() error { return nil }, nil
			}
			if phase == "upload-intent" {
				r, err := service.RetryRecovery(context.Background(), o)
				var blocked *RecoveryBlockedError
				if !errors.As(err, &blocked) || r.Reason != "uncertain-write" {
					t.Fatalf("uncertain resolution was replayed: %+v %v", r, err)
				}
				setRecoveredHead(t, ex, base, OpUpload)
			}
			report, err := service.RetryRecovery(context.Background(), o)
			if err != nil || report.Pending {
				t.Fatalf("resolution restart: %+v %v", report, err)
			}
			restored, err := loadState(o.ProfileDir, state.Binding)
			if err != nil || restored.Transaction != nil {
				t.Fatalf("restart state: %v", err)
			}
			presend := phase == "staged" || phase == "pin-intent" || phase == "pinned"
			if presend {
				if len(restored.Conflicts) != 1 || report.Outcome != "conflict-pending" {
					t.Fatal("original conflict lost during pre-send retirement")
				}
			} else {
				if len(restored.Conflicts) != 0 || report.Outcome != "completed" {
					t.Fatal("resolved conflict retained")
				}
			}
			for _, path := range []string{c.LocalStagePath, c.CloudStagePath} {
				_, err = os.Stat(filepath.Join(o.ProfileDir, path))
				if presend && err != nil {
					t.Fatal("pending candidate removed")
				}
				if !presend && !errors.Is(err, os.ErrNotExist) {
					t.Fatal("resolved candidate cleanup incomplete")
				}
			}
		})
	}
}

type reviewCaptureMovesHeadAPI struct{ *uploadAPI }

func (a *reviewCaptureMovesHeadAPI) DownloadRevision(ctx context.Context, id, rev string, w io.Writer) error {
	if err := a.uploadAPI.DownloadRevision(ctx, id, rev, w); err != nil {
		return err
	}
	a.file.HeadRevisionID = "rev-3"
	a.file.ModifiedTime = "2026-01-03T00:00:00Z"
	return nil
}
func TestRecoveryCaptureRechecksHead(t *testing.T) {
	_, _, state, ex, base := recoveryFixture(t, OpUpload)
	setRecoveredHead(t, ex, base, OpUpload)
	reviewCompetitor(base)
	api := &reviewCaptureMovesHeadAPI{base}
	ex.api, ex.mutation = api, api
	if err := ex.recoverUpload(context.Background()); err == nil {
		t.Fatalf("cleared transaction with stale baseline %s while remote head is %s; transaction retained=%v", state.Completed[0].HeadRevisionID, base.file.HeadRevisionID, state.Transaction != nil)
	}
}

func TestRecoveryCapturePreservesPinAndDownloadFailures(t *testing.T) {
	for _, mode := range []string{"pin", "download"} {
		t.Run(mode, func(t *testing.T) {
			_, _, state, ex, base := recoveryFixture(t, OpUpload)
			setRecoveredHead(t, ex, base, OpUpload)
			reviewCompetitor(base)
			sentinel := errors.New("capture failure")
			if mode == "pin" {
				base.pinErr = sentinel
			} else {
				base.revisionDownloadErr = sentinel
			}
			if err := ex.recoverUpload(context.Background()); !errors.Is(err, sentinel) || state.Transaction == nil {
				t.Fatalf("capture cause lost: %v", err)
			}
		})
	}
}

type reviewPinFailureAPI struct {
	*uploadAPI
	calls    int
	sentinel error
}

func (a *reviewPinFailureAPI) SetRevisionKeepForever(ctx context.Context, id, revision string, keep bool) error {
	a.calls++
	if a.calls == 2 {
		return a.sentinel
	}
	return a.uploadAPI.SetRevisionKeepForever(ctx, id, revision, keep)
}
func TestRecoveryCaptureSecondPinPreservesCause(t *testing.T) {
	_, _, state, ex, base := recoveryFixture(t, OpUpload)
	setRecoveredHead(t, ex, base, OpUpload)
	reviewCompetitor(base)
	sentinel := errors.New("second pin failure")
	api := &reviewPinFailureAPI{uploadAPI: base, sentinel: sentinel}
	ex.api, ex.mutation = api, api
	if err := ex.recoverUpload(context.Background()); !errors.Is(err, sentinel) || state.Transaction == nil {
		t.Fatalf("pin cause lost: %v", err)
	}
}
