package fixture

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/torfstack/grove/internal/privatefs"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

func VerifyLocal(ctx context.Context, v Verified, localDir string) (Report, error) {
	paths, err := manifestPaths(v.Manifest)
	if err != nil {
		return Report{}, err
	}
	localDir, err = privatefs.Canonical(localDir)
	if err != nil {
		return Report{}, err
	}
	if err = privatefs.NoSymlinks(localDir); err != nil {
		return Report{}, err
	}
	expected := map[string]Entry{}
	for _, e := range v.Manifest.Entries {
		expected[paths[e.ID]] = e
	}
	report := Report{}
	err = filepath.WalkDir(localDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return errors.New("cannot inspect local fixture")
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(localDir, path)
		if err != nil {
			return err
		}
		e, ok := expected[rel]
		if !ok {
			return errors.New("unexpected local fixture entry")
		}
		delete(expected, rel)
		if d.Type()&os.ModeSymlink != 0 {
			return errors.New("local fixture symlink")
		}
		if e.Kind == "folder" {
			if !d.IsDir() {
				return errors.New("fixture folder type mismatch")
			}
			if rel != "." {
				report.Directories++
			}
			return nil
		}
		info, err := d.Info()
		if err != nil || !info.Mode().IsRegular() || info.Size() != e.Size {
			return errors.New("fixture file size or type mismatch")
		}
		f, err := os.Open(path)
		if err != nil {
			return errors.New("cannot open local fixture file")
		}
		hash := sha256.New()
		_, readErr := io.Copy(hash, f)
		closeErr := f.Close()
		if readErr != nil || closeErr != nil {
			return errors.New("cannot read local fixture file")
		}
		if hex.EncodeToString(hash.Sum(nil)) != e.SHA256 {
			return errors.New("fixture file hash mismatch")
		}
		report.Files++
		return nil
	})
	if err != nil {
		return Report{}, err
	}
	if len(expected) != 0 {
		return Report{}, errors.New("missing local fixture entries")
	}
	return report, nil
}
