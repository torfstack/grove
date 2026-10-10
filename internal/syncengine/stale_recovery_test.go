package syncengine

import (
	"context"
	"errors"
	"testing"

	"github.com/torfstack/grove/internal/drive"
)

type metadataErrorAPI struct {
	drive.API
	err error
}

func (a metadataErrorAPI) Get(context.Context, string) (drive.File, error) {
	return drive.File{}, a.err
}

func TestStaleMoveRecovery(t *testing.T) {
	for _, remoteChanged := range []bool{true, false} {
		t.Run(map[bool]string{true: "changed", false: "API error"}[remoteChanged], func(t *testing.T) {
			ex, op, profile := moving(t)
			if err := ex.journal(Journal{Operation: op, Phase: "intent", After: remapBaseline(op.Before, "old", "new")}); err != nil {
				t.Fatal(err)
			}
			apiErr := errors.New("remote unavailable")
			if remoteChanged {
				changed := op.Entry.Remote
				changed.Name = "newer"
				ex.api = moveAPI{entry: changed}
			} else {
				ex.api = metadataErrorAPI{API: ex.api, err: apiErr}
			}
			err := ex.recover(context.Background(), Snapshot{})
			if remoteChanged {
				if err != nil || ex.state.Transaction != nil {
					t.Fatal("stale untouched intent not cleared", err)
				}
			} else if !errors.Is(err, apiErr) || ex.state.Transaction == nil {
				t.Fatal("API error discarded intent", err)
			}
			if present, err := ex.inspectSubtree(context.Background(), "old", op.Before); !present || err != nil {
				t.Fatal("source changed", err)
			}
			if present, err := ex.inspectSubtree(context.Background(), "new", remapBaseline(op.Before, "old", "new")); present || err != nil {
				t.Fatal("stale move executed", err)
			}
			state, err := loadState(profile, Binding{})
			if err != nil || (state.Transaction == nil) != remoteChanged {
				t.Fatal("durable intent differs", err)
			}
		})
	}
}

func TestStaleReplacementRecovery(t *testing.T) {
	for _, remoteChanged := range []bool{true, false} {
		t.Run(map[bool]string{true: "changed", false: "API error"}[remoteChanged], func(t *testing.T) {
			ex, op, profile := replacement(t)
			j := Journal{Operation: op, Phase: "intent", TempPath: ".grove-download-stale", BackupPath: ".grove-backup-stale"}
			if err := ex.journal(j); err != nil {
				t.Fatal(err)
			}
			hash, err := ex.stageDownload(context.Background(), op.Entry, j.TempPath)
			if err != nil {
				t.Fatal(err)
			}
			j = *ex.state.Transaction
			j.Phase = "verified"
			j.VerifiedSHA256 = hash
			j.After = []Completed{completedEntry(op.Entry, hash)}
			if err = ex.journal(j); err != nil {
				t.Fatal(err)
			}
			apiErr := errors.New("remote unavailable")
			if remoteChanged {
				ex.api.(*downloadAPI).children[0].Version = "3"
			} else {
				ex.api = metadataErrorAPI{API: ex.api, err: apiErr}
			}
			err = ex.recover(context.Background(), Snapshot{})
			if remoteChanged {
				if err != nil || ex.state.Transaction != nil {
					t.Fatal("stale unpublished replacement not cleared", err)
				}
			} else if !errors.Is(err, apiErr) || ex.state.Transaction == nil {
				t.Fatal("API error discarded stage", err)
			}
			if err = ex.requireFile(context.Background(), op.Entry.Path, op.Before[0]); err != nil {
				t.Fatal("old bytes changed", err)
			}
			present, err := ex.inspectFile(context.Background(), j.TempPath, j.After[0])
			if err != nil || present == remoteChanged {
				t.Fatal("wrong stage cleanup", err)
			}
			state, err := loadState(profile, Binding{})
			if err != nil || (state.Transaction == nil) != remoteChanged {
				t.Fatal("durable intent differs", err)
			}
		})
	}
}

func TestStaleMoveDuringExecutionStopsPlan(t *testing.T) {
	ex, op, _ := moving(t)
	changed := op.Entry.Remote
	changed.Name = "later"
	ex.api = moveAPI{entry: changed}
	if err := ex.execute(context.Background(), op); !errors.Is(err, errPendingRemoteChanged) {
		t.Fatal("stale execution reported success", err)
	}
	if ex.state.Transaction != nil {
		t.Fatal("untouched stale intent retained")
	}
	if present, err := ex.inspectSubtree(context.Background(), "old", op.Before); !present || err != nil {
		t.Fatal("source changed", err)
	}
}

func TestReplacementCancellationRecovery(t *testing.T) {
	for _, failAt := range []int{1, 2} {
		t.Run(map[int]string{1: "cancellation save", 2: "clear save"}[failAt], func(t *testing.T) {
			ex, op, profile := replacement(t)
			j := Journal{Operation: op, Phase: "intent", TempPath: ".grove-download-cancel", BackupPath: ".grove-backup-cancel"}
			if err := ex.journal(j); err != nil {
				t.Fatal(err)
			}
			hash, err := ex.stageDownload(context.Background(), op.Entry, j.TempPath)
			if err != nil {
				t.Fatal(err)
			}
			j = *ex.state.Transaction
			j.Phase = "verified"
			j.VerifiedSHA256 = hash
			j.After = []Completed{completedEntry(op.Entry, hash)}
			if err = ex.journal(j); err != nil {
				t.Fatal(err)
			}
			ex.api.(*downloadAPI).children[0].Version = "3"
			save := ex.save
			calls := 0
			ex.save = func() error {
				calls++
				if calls == failAt {
					return errors.New("save failure")
				}
				return save()
			}
			if err = ex.recover(context.Background(), Snapshot{}); err == nil {
				t.Fatal("save fault ignored")
			}
			state, err := loadState(profile, Binding{})
			if err != nil {
				t.Fatal(err)
			}
			*ex.state = state
			ex.save = save
			if failAt == 2 {
				ex.api = metadataErrorAPI{API: ex.api, err: errors.New("remote unavailable")}
			}
			if err = ex.recover(context.Background(), Snapshot{}); err != nil {
				t.Fatal("cancelled cleanup cannot resume", err)
			}
			if ex.state.Transaction != nil {
				t.Fatal("cancelled journal retained")
			}
			if err = ex.requireFile(context.Background(), op.Entry.Path, op.Before[0]); err != nil {
				t.Fatal("old content lost", err)
			}
		})
	}
}

func TestStaleReplacementAfterBackupPreserved(t *testing.T) {
	ex, op, _ := replacement(t)
	j := Journal{Operation: op, Phase: "intent", TempPath: ".grove-download-backed", BackupPath: ".grove-backup-backed"}
	if err := ex.journal(j); err != nil {
		t.Fatal(err)
	}
	hash, err := ex.stageDownload(context.Background(), op.Entry, j.TempPath)
	if err != nil {
		t.Fatal(err)
	}
	j = *ex.state.Transaction
	j.Phase = "verified"
	j.VerifiedSHA256 = hash
	j.After = []Completed{completedEntry(op.Entry, hash)}
	if err = ex.journal(j); err != nil {
		t.Fatal(err)
	}
	if err = moveNoReplace(ex.root, op.Entry.Path, j.BackupPath); err != nil {
		t.Fatal(err)
	}
	ex.api.(*downloadAPI).children[0].Version = "3"
	if err = ex.recover(context.Background(), Snapshot{}); !errors.Is(err, errPendingRemoteChanged) {
		t.Fatal("stale staged content accepted", err)
	}
	if ex.state.Transaction == nil {
		t.Fatal("changed tracked paths discarded journal")
	}
	if err = ex.requireFile(context.Background(), j.BackupPath, op.Before[0]); err != nil {
		t.Fatal("backup lost", err)
	}
	if err = ex.requireFile(context.Background(), j.TempPath, j.After[0]); err != nil {
		t.Fatal("stage lost", err)
	}
}
