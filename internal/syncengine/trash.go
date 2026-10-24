package syncengine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/torfstack/grove/internal/drive"
)

type uncertainTrashError struct{ cause error }

func (e uncertainTrashError) Error() string { return "remote trash outcome is uncertain" }
func (e uncertainTrashError) Unwrap() error { return e.cause }

func (e *executor) trash(ctx context.Context, op Operation) error {
	if e.mutation == nil {
		return errors.New("two-way sync requires a mutation-capable Drive API")
	}
	if len(op.Before) == 0 || op.Before[0].RemoteID != op.Entry.Remote.ID || len(op.RemoteSubtree) == 0 {
		return errors.New("trash operation has no tracked baseline")
	}
	remoteIDs := map[string]bool{}
	for _, entry := range op.RemoteSubtree {
		remoteIDs[entry.RemoteID] = true
	}
	for _, entry := range op.Before {
		if !remoteIDs[entry.RemoteID] {
			return errors.New("tracked deletion descendant moved outside the reviewed remote subtree")
		}
	}
	if err := e.journal(Journal{Operation: op, Phase: "intent"}); err != nil {
		return err
	}
	snapshot, err := Scan(ctx, e.api, e.remoteRoot)
	if err != nil {
		return e.retireUnsentTrash(err)
	}
	remoteByID := make(map[string]Entry, len(snapshot.Entries))
	for _, entry := range snapshot.Entries {
		remoteByID[entry.Remote.ID] = entry
	}
	root, ok := remoteByID[op.Entry.Remote.ID]
	if !ok || root.Path != op.Entry.Path || !sameDeletionRemoteSubtree(op.RemoteSubtree, remoteDeletionSubtree(snapshot.Entries, op.Entry.Path)) {
		return e.retireUnsentTrash(errPendingRemoteChanged)
	}
	if err := e.currentRemote(ctx, op.Entry); err != nil {
		return e.retireUnsentTrash(err)
	}
	if err := rootPath(e.root, op.Before[0].Path); err != nil {
		return e.retireUnsentTrash(err)
	}
	if _, err := e.root.Lstat(op.Before[0].Path); !errors.Is(err, os.ErrNotExist) {
		return e.retireUnsentTrash(errors.New("local deletion path reappeared or cannot be inspected"))
	}
	j := *e.state.Transaction
	j.Phase = "trash-intent"
	if err := e.journal(j); err != nil {
		return err
	}
	if err := e.mutation.Trash(ctx, op.Entry.Remote.ID); err != nil {
		if errors.Is(err, drive.ErrMutationRejected) {
			return e.retireUnsentTrash(err)
		}
		return uncertainTrashError{cause: err}
	}
	return e.recoverTrash(ctx)
}

func (e *executor) retireUnsentTrash(cause error) error {
	if err := e.clearJournal(); err != nil {
		return err
	}
	return cause
}

func (e *executor) recoverTrash(ctx context.Context) error {
	j := *e.state.Transaction
	if j.Operation.Kind != OpTrash || len(j.Operation.Before) == 0 {
		return errors.New("invalid pending trash operation")
	}
	current, err := e.api.Get(ctx, j.Operation.Entry.Remote.ID)
	if err != nil {
		return err
	}
	if current.ID != j.Operation.Entry.Remote.ID {
		return errors.New("remote trash identity is not confirmed; transaction remains blocked")
	}
	if !current.ExplicitlyTrashed {
		if j.Phase == "intent" && !current.Trashed {
			if err = e.clearJournal(); err != nil {
				return err
			}
			return errPendingRemoteChanged
		}
		return errors.New("remote trash outcome is not confirmed; transaction remains blocked")
	}
	byPath := make(map[string]Completed, len(j.Operation.RemoteSubtree))
	for _, entry := range j.Operation.RemoteSubtree {
		byPath[entry.Path] = entry
	}
	rootBaseline, found := byPath[j.Operation.Entry.Path]
	if !found || len(j.Operation.Entry.Remote.Parents) != 1 || !trashedFingerprintMatches(current, rootBaseline, j.Operation.Entry.Remote.Parents[0]) {
		return errors.New("trashed remote root changed; transaction remains blocked")
	}
	for _, descendant := range j.Operation.RemoteSubtree {
		if descendant.RemoteID == j.Operation.Entry.Remote.ID {
			continue
		}
		child, getErr := e.api.Get(ctx, descendant.RemoteID)
		if getErr != nil {
			return failedPrivately("remote trash subtree is not confirmed; transaction remains blocked", getErr)
		}
		if child.ID != descendant.RemoteID {
			return errors.New("remote trash descendant identity is not confirmed; transaction remains blocked")
		}
		if !child.Trashed || child.ExplicitlyTrashed {
			return fmt.Errorf("remote trash descendant state is not inherited: trashed=%t explicitlyTrashed=%t", child.Trashed, child.ExplicitlyTrashed)
		}
		parent, found := byPath[filepath.Dir(descendant.Path)]
		if !found || !trashedFingerprintMatches(child, descendant, parent.RemoteID) {
			return errors.New("trashed remote descendant changed; transaction remains blocked")
		}
	}
	if j.Phase != "committed" {
		if err := e.commitJournal(j); err != nil {
			return err
		}
	}
	return e.clearJournal()
}

func trashedFingerprintMatches(current drive.File, expected Completed, parentID string) bool {
	return current.ID == expected.RemoteID && current.Trashed && current.OwnedByMe && current.DriveID == "" && current.Name == filepath.Base(expected.Path) && entryKind(Entry{Remote: current}) == expected.Kind && len(current.Parents) == 1 && current.Parents[0] == parentID && current.ModifiedTime == expected.ModifiedTime && current.HeadRevisionID == expected.HeadRevisionID && current.MD5 == expected.MD5 && current.Size == expected.Size
}
