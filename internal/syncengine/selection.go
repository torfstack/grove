package syncengine

import (
	"context"
	"errors"
	"path/filepath"
	"sort"

	"github.com/torfstack/grove/internal/drive"
	"github.com/torfstack/grove/internal/privatefs"
)

type SelectionFolder struct {
	ID   string
	Path string
}

func (s Service) DiscoverFolders(ctx context.Context, rootID, token string) (folders []SelectionFolder, err error) {
	if rootID == "" || token == "" {
		return nil, errors.New("remote root and token file are required")
	}
	open := s.OpenAPI
	if open == nil {
		open = defaultOpenAPI
	}
	api, closeSession, err := open(ctx, token, "read-only")
	if err != nil {
		return nil, err
	}
	defer closeResult(closeSession, &err)
	return discoverSelectionFolders(ctx, api, rootID)
}

func discoverSelectionFolders(ctx context.Context, api drive.API, rootID string) ([]SelectionFolder, error) {
	root, err := api.Get(ctx, rootID)
	if err != nil {
		return nil, err
	}
	if root.ID != rootID || !ownedSelectionFolder(root) {
		return nil, errors.New("remote root must be an owned My Drive folder")
	}
	seen := map[string]bool{rootID: true}
	var folders []SelectionFolder
	var walk func(string, string) error
	walk = func(parent, path string) error {
		files, err := drive.ListAll(ctx, api, drive.ChildrenQuery(parent))
		if err != nil {
			return err
		}
		names := make(map[string]int)
		for _, file := range files {
			names[file.Name]++
		}
		for _, file := range files {
			if err := ctx.Err(); err != nil {
				return err
			}
			if !ownedSelectionFolder(file) {
				continue
			}
			if file.ID == "" || seen[file.ID] || !privatefs.SafeName(file.Name) || names[file.Name] != 1 || len(file.Parents) != 1 || file.Parents[0] != parent {
				return errors.New("invalid selectable folder identity, name, or ancestry")
			}
			seen[file.ID] = true
			child := filepath.Join(path, file.Name)
			folders = append(folders, SelectionFolder{ID: file.ID, Path: child})
			if err := walk(file.ID, child); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(rootID, ""); err != nil {
		return nil, err
	}
	sort.Slice(folders, func(i, j int) bool { return folders[i].Path < folders[j].Path })
	return folders, nil
}

func ownedSelectionFolder(file drive.File) bool {
	return file.MIMEType == drive.FolderMIME && file.OwnedByMe && file.DriveID == "" && !file.Trashed && !file.ExplicitlyTrashed
}

func selectedLocalScope(snapshot Snapshot, ids []string) func(string) bool {
	selected := make(map[string]bool, len(ids))
	for _, id := range ids {
		selected[id] = true
	}
	ancestors := make(map[string]bool)
	var roots []string
	for _, entry := range snapshot.Entries {
		if selected[entry.Remote.ID] {
			roots = append(roots, entry.Path)
			for path := filepath.Dir(entry.Path); path != "."; path = filepath.Dir(path) {
				ancestors[path] = true
			}
		}
	}
	return func(path string) bool {
		if ancestors[path] {
			return true
		}
		for _, root := range roots {
			if within(root, path) {
				return true
			}
		}
		return false
	}
}

func (s Service) ReplaceSelection(ctx context.Context, profile string, ids []string) (err error) {
	if profile == "" {
		return errors.New("profile directory is required")
	}
	profile, err = privatefs.Canonical(profile)
	if err != nil {
		return err
	}
	if err := privatefs.OutsideGit(profile); err != nil {
		return err
	}
	lease, err := privatefs.AcquireLease(filepath.Join(profile, "daemon.lock"))
	if err != nil {
		return err
	}
	defer closeResult(lease.Close, &err)
	opts, state, lock, err := existingProfile(ctx, profile)
	if err != nil {
		return err
	}
	defer closeResult(lock.Close, &err)
	if !state.PopulationComplete || state.Pending != nil || state.Transaction != nil || state.ProbePath != "" || len(state.Conflicts) != 0 {
		return errors.New("complete population and resolve pending recovery or conflicts before changing selection")
	}
	if state.selectionRevision() == ^uint64(0) {
		return errors.New("selection revision exhausted")
	}
	selection := &Selection{Mode: "all", Revision: state.selectionRevision() + 1}
	if ids != nil {
		selection.Mode = "include"
		selection.FolderIDs = ids
	}
	state.Selection = selection
	if _, err := validateState(state, state.Binding); err != nil {
		return err
	}
	if len(ids) != 0 {
		if err := s.verifySelection(ctx, opts, ids); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return saveState(profile, state)
}

func (s Service) verifySelection(ctx context.Context, opts Options, ids []string) (err error) {
	open := s.OpenAPI
	if open == nil {
		open = defaultOpenAPI
	}
	api, closeSession, err := open(ctx, opts.TokenFile, "read-only")
	if err != nil {
		return err
	}
	defer closeResult(closeSession, &err)
	_, err = scanSelectedFolders(ctx, api, opts.RemoteRoot, ids)
	return err
}

func scopedBaseline(state State, snapshot Snapshot, includes func(string) bool) State {
	visible := make(map[string]bool, len(snapshot.Entries))
	for _, entry := range snapshot.Entries {
		visible[entry.Remote.ID] = true
	}
	var completed []Completed
	for _, entry := range state.Completed {
		if visible[entry.RemoteID] || includes(entry.Path) {
			completed = append(completed, entry)
		}
	}
	state.Completed = completed
	return state
}
