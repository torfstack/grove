package syncengine

import (
	"context"
	"encoding/hex"
	"errors"
	"github.com/torfstack/grove/internal/drive"
	"github.com/torfstack/grove/internal/privatefs"
	"path/filepath"
	"sort"
	"strings"
)

func Scan(ctx context.Context, api drive.API, rootID string) (Snapshot, error) {
	return scanRemote(ctx, api, rootID, nil)
}

func scanSelectedFolders(ctx context.Context, api drive.API, rootID string, ids []string) (Snapshot, error) {
	scope := make(map[string]SelectionFolder)
	if len(ids) == 0 {
		return scanRemote(ctx, api, rootID, scope)
	}
	folders, err := discoverSelectionFolders(ctx, api, rootID)
	if err != nil {
		return Snapshot{}, err
	}
	byID := make(map[string]string, len(folders))
	byPath := make(map[string]string, len(folders))
	for _, folder := range folders {
		byID[folder.ID] = folder.Path
		byPath[folder.Path] = folder.ID
	}
	for _, id := range ids {
		path, ok := byID[id]
		if !ok {
			return Snapshot{}, errors.New("selected folder is missing or outside the bound root")
		}
		scope[path] = SelectionFolder{ID: id, Path: path}
	}
	for _, id := range ids {
		for path := filepath.Dir(byID[id]); path != "."; path = filepath.Dir(path) {
			if _, ok := scope[path]; !ok {
				scope[path] = SelectionFolder{ID: byPath[path]}
			}
		}
	}
	return scanRemote(ctx, api, rootID, scope)
}

func scanRemote(ctx context.Context, api drive.API, rootID string, scope map[string]SelectionFolder) (Snapshot, error) {
	root, err := api.Get(ctx, rootID)
	if err != nil {
		return Snapshot{}, err
	}
	if root.ID != rootID || root.MIMEType != drive.FolderMIME || !root.OwnedByMe || root.DriveID != "" || root.Trashed || root.ExplicitlyTrashed {
		return Snapshot{}, errors.New("remote root must be an owned My Drive folder")
	}
	seen := map[string]bool{rootID: true}
	out := Snapshot{}
	verified := make(map[string]bool)
	var walk func(string, string, bool) error
	walk = func(id, path string, full bool) error {
		files, err := drive.ListAll(ctx, api, drive.ChildrenQuery(id))
		if err != nil {
			return err
		}
		names := map[string]bool{}
		counts := make(map[string]int)
		for _, file := range files {
			counts[file.Name]++
		}
		sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
		for _, f := range files {
			if err := ctx.Err(); err != nil {
				return err
			}
			child := filepath.Join(path, f.Name)
			target, needed := scope[child]
			if !full && !needed {
				continue
			}
			if needed {
				if target.ID != f.ID || f.MIMEType != drive.FolderMIME || counts[f.Name] != 1 {
					return errors.New("selected folder ancestry changed during scan")
				}
				verified[child] = true
			}
			if !privatefs.SafeName(f.Name) || names[f.Name] || seen[f.ID] || f.ID == "" {
				return errors.New("unsafe or duplicate remote name or identity")
			}
			names[f.Name] = true
			seen[f.ID] = true
			if !f.OwnedByMe || f.DriveID != "" || f.Trashed || f.ExplicitlyTrashed || len(f.Parents) != 1 || f.Parents[0] != id {
				return errors.New("unsupported remote ownership or parent")
			}
			if f.MIMEType != drive.FolderMIME {
				sum, err := hex.DecodeString(f.MD5)
				if strings.HasPrefix(f.MIMEType, "application/vnd.google-apps.") || !f.HasSize || f.Size < 0 || !f.CanDownload || err != nil || len(sum) != 16 || f.Version == "" {
					return errors.New("unsupported remote file or missing metadata")
				}
			}
			entry := Entry{Path: filepath.Join(path, f.Name), Remote: f}
			out.Entries = append(out.Entries, entry)
			if f.MIMEType == drive.FolderMIME {
				if err = walk(f.ID, entry.Path, full || target.Path != ""); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err = walk(rootID, "", scope == nil); err != nil {
		return Snapshot{}, err
	}
	if len(verified) != len(scope) {
		return Snapshot{}, errors.New("selected folder ancestry changed during scan")
	}
	return out, nil
}
