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
	root, err := api.Get(ctx, rootID)
	if err != nil {
		return Snapshot{}, err
	}
	if root.ID != rootID || root.MIMEType != drive.FolderMIME || !root.OwnedByMe || root.DriveID != "" || root.Trashed {
		return Snapshot{}, errors.New("remote root must be an owned My Drive folder")
	}
	seen := map[string]bool{rootID: true}
	out := Snapshot{}
	var walk func(string, string) error
	walk = func(id, path string) error {
		files, err := drive.ListAll(ctx, api, drive.ChildrenQuery(id))
		if err != nil {
			return err
		}
		names := map[string]bool{}
		sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
		for _, f := range files {
			if err := ctx.Err(); err != nil {
				return err
			}
			if !privatefs.SafeName(f.Name) || names[f.Name] || seen[f.ID] || f.ID == "" {
				return errors.New("unsafe or duplicate remote name or identity")
			}
			names[f.Name] = true
			seen[f.ID] = true
			if !f.OwnedByMe || f.DriveID != "" || f.Trashed || len(f.Parents) != 1 || f.Parents[0] != id {
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
				if err = walk(f.ID, entry.Path); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err = walk(rootID, ""); err != nil {
		return Snapshot{}, err
	}
	return out, nil
}
