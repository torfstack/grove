package syncengine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
)

func (e *executor) replace(ctx context.Context, op Operation) error {
	if len(op.Before) != 1 || op.Before[0].Kind != "file" || op.Before[0].Path != op.Entry.Path {
		return errors.New("invalid replacement baseline")
	}
	if err := e.requireFile(ctx, op.Entry.Path, op.Before[0]); err != nil {
		return err
	}
	id := nonce()
	if id == "" {
		return errors.New("cannot create replacement identity")
	}
	j := Journal{Operation: op, Phase: "intent", TempPath: filepath.Join(filepath.Dir(op.Entry.Path), ".grove-download-"+id), BackupPath: filepath.Join(filepath.Dir(op.Entry.Path), ".grove-backup-"+id)}
	if err := e.journal(j); err != nil {
		return err
	}
	hash, err := e.stageDownload(ctx, op.Entry, j.TempPath)
	if err != nil {
		return err
	}
	j = *e.state.Transaction
	j.Phase = "verified"
	j.VerifiedSHA256 = hash
	j.After = []Completed{completedEntry(op.Entry, hash)}
	if err = e.journal(j); err != nil {
		return err
	}
	return e.recoverReplace(ctx)
}
func (e *executor) recoverReplace(ctx context.Context) error {
	j := *e.state.Transaction
	old := j.Operation.Before[0]
	target := j.Operation.Entry.Path
	if j.Phase == "intent" {
		if err := e.requireFile(ctx, target, old); err != nil {
			return err
		}
		if _, err := e.root.Lstat(j.BackupPath); !errors.Is(err, os.ErrNotExist) {
			return errors.New("unexpected replacement backup")
		}
		if err := e.removeTemp(ctx, j.TempPath, j.TempIdentity, "", 0, false); err != nil {
			return err
		}
		if err := e.parentSync(j.TempPath); err != nil {
			return err
		}
		return e.clearJournal()
	}
	if len(j.After) != 1 {
		return errors.New("replacement has no verified baseline")
	}
	next := j.After[0]
	backup, err := e.inspectFile(ctx, j.BackupPath, old)
	if err != nil {
		return err
	}
	stage, err := e.inspectFile(ctx, j.TempPath, next)
	if err != nil {
		return err
	}
	present, newErr := e.inspectFile(ctx, target, next)
	if present && newErr == nil {
		if j.Phase != "committed" {
			if !backup {
				return errors.New("replacement backup missing")
			}
			if err = e.parentSync(target); err != nil {
				return err
			}
			if err = e.commitJournal(j); err != nil {
				return err
			}
		}
		if err = e.removeVerified(ctx, j.BackupPath, old); err != nil {
			return err
		}
		if err = e.removeVerified(ctx, j.TempPath, next); err != nil {
			return err
		}
		return e.clearJournal()
	}
	if j.Phase == "committed" {
		return errors.New("committed replacement content changed")
	}
	if !stage {
		return errors.New("verified replacement stage missing")
	}
	if err = e.currentRemote(ctx, j.Operation.Entry); err != nil {
		return err
	}
	if present {
		if backup {
			return errors.New("replacement source and backup both present")
		}
		if err = e.requireFile(ctx, target, old); err != nil {
			return err
		}
		if err = moveNoReplace(e.root, target, j.BackupPath); err != nil {
			return err
		}
		if err = e.parentSync(target); err != nil {
			return err
		}
		j.Phase = "backed-up"
		if err = e.journal(j); err != nil {
			return err
		}
		if err = e.requireFile(ctx, j.BackupPath, old); err != nil {
			return err
		}
	} else if !backup {
		return errors.New("replacement source and backup missing")
	}
	if err = publish(e.root, j.TempPath, target); err != nil {
		return err
	}
	if err = e.parentSync(target); err != nil {
		return err
	}
	if err = e.requireFile(ctx, target, next); err != nil {
		return err
	}
	if err = e.commitJournal(j); err != nil {
		return err
	}
	if err = e.removeVerified(ctx, j.BackupPath, old); err != nil {
		return err
	}
	return e.clearJournal()
}
