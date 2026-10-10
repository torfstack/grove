package fixture

import (
	"context"
	"errors"
	"github.com/torfstack/grove/internal/drive"
	"sort"
	"strings"
)

// Cleanup requires the caller to hold the run lock before opening a token session.
func Cleanup(ctx context.Context, api drive.API, dir string) error {
	r, err := LoadRun(dir)
	if err != nil {
		return err
	}
	if err = reconcile(ctx, api, dir, &r); err != nil {
		return err
	}
	for _, object := range r.Objects {
		if object.Status == "pending" {
			return errors.New("fixture create outcome remains unresolved; cleanup refused")
		}
	}
	files, err := membership(ctx, api, r)
	if err != nil {
		return err
	}
	paths, err := manifestPaths(r.Manifest)
	if err != nil {
		return err
	}
	indices := make([]int, len(r.Objects))
	for i := range indices {
		indices[i] = i
	}
	sort.Slice(indices, func(i, j int) bool {
		left, right := paths[r.Objects[indices[i]].LogicalID], paths[r.Objects[indices[j]].LogicalID]
		if left == "." || right == "." {
			return left != "." && right == "."
		}
		leftDepth, rightDepth := strings.Count(left, "/"), strings.Count(right, "/")
		return leftDepth > rightDepth || (leftDepth == rightDepth && left > right)
	})
	for _, i := range indices {
		o := r.Objects[i]
		if o.Status == "trashed" {
			continue
		}
		if _, ok := files[o.LogicalID]; ok {
			f, err := api.Get(ctx, o.RemoteID)
			if err != nil && !errors.Is(err, drive.ErrNotFound) {
				return err
			}
			if err == nil && !f.Trashed {
				if !owned(f, r, o) {
					return errors.New("fixture ownership changed during cleanup")
				}
				if f.MIMEType == drive.FolderMIME {
					children, err := drive.ListAll(ctx, api, drive.ChildrenQuery(f.ID))
					if err != nil {
						return err
					}
					if len(children) != 0 {
						return errors.New("fixture folder is not empty; cleanup refused")
					}
				}
				if err = api.Trash(ctx, f.ID); err != nil && !errors.Is(err, drive.ErrNotFound) {
					return err
				}
			}
		}
		r.Objects[i].Status = "trashed"
		if err = saveRun(dir, r); err != nil {
			return err
		}
	}
	return nil
}
