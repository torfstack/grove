package syncengine

import (
	"context"
	"errors"
	"os"
	"sort"
)

func moveSource(op Operation) (Completed, error) {
	for _, c := range op.Before {
		if c.RemoteID == op.Entry.Remote.ID {
			return c, nil
		}
	}
	return Completed{}, errors.New("move source baseline missing")
}
func (e *executor) inspectSubtree(ctx context.Context, path string, expected []Completed) (bool, error) {
	if err := rootPath(e.root, path); err != nil {
		return false, err
	}
	info, err := e.root.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return true, errors.New("cannot inspect moved subtree")
	}
	if len(expected) == 1 && expected[0].Kind == "file" {
		return e.inspectFile(ctx, path, expected[0])
	}
	if !info.IsDir() {
		return true, errors.New("moved subtree type changed")
	}
	// Scan only the selected subtree through a rooted handle; unrelated paths do not affect recovery.
	sub, err := e.root.OpenRoot(path)
	if err != nil {
		return true, errors.New("cannot open moved subtree")
	}
	defer func() { _ = sub.Close() }()
	local, err := scanLocal(ctx, sub, nil)
	if err != nil {
		return true, err
	}
	local = append(local, LocalEntry{Path: path, Kind: "folder"})
	for i := 0; i < len(local)-1; i++ {
		local[i].Path = path + string(os.PathSeparator) + local[i].Path
	}
	return true, validateLocal(local, expected)
}
func (e *executor) move(ctx context.Context, op Operation) error {
	source, err := moveSource(op)
	if err != nil {
		return err
	}
	present, err := e.inspectSubtree(ctx, source.Path, op.Before)
	if err != nil {
		return err
	}
	if !present {
		return errors.New("move source missing")
	}
	after := remapBaseline(op.Before, source.Path, op.Entry.Path)
	sort.Slice(after, func(i, j int) bool { return after[i].Path < after[j].Path })
	if err = e.journal(Journal{Operation: op, Phase: "intent", After: after}); err != nil {
		return err
	}
	return e.recoverMove(ctx)
}
func (e *executor) recoverMove(ctx context.Context) error {
	j := *e.state.Transaction
	source, err := moveSource(j.Operation)
	if err != nil {
		return err
	}
	from, fromErr := e.inspectSubtree(ctx, source.Path, j.Operation.Before)
	to, toErr := e.inspectSubtree(ctx, j.Operation.Entry.Path, j.After)
	if fromErr != nil {
		return fromErr
	}
	if toErr != nil {
		return toErr
	}
	if from == to {
		return errors.New("ambiguous move source and destination")
	}
	if from {
		if j.Phase == "committed" {
			return errors.New("committed move source reappeared")
		}
		if err = e.currentRemote(ctx, j.Operation.Entry); err != nil {
			if errors.Is(err, errPendingRemoteChanged) {
				if clearErr := e.clearJournal(); clearErr != nil {
					return clearErr
				}
			}
			return err
		}
		if err = moveNoReplace(e.root, source.Path, j.Operation.Entry.Path); err != nil {
			return err
		}
	}
	if err = e.parentSync(source.Path); err != nil {
		return err
	}
	if err = e.parentSync(j.Operation.Entry.Path); err != nil {
		return err
	}
	if _, err = e.inspectSubtree(ctx, j.Operation.Entry.Path, j.After); err != nil {
		return err
	}
	if j.Phase != "committed" {
		if err = e.commitJournal(j); err != nil {
			return err
		}
	}
	return e.clearJournal()
}
