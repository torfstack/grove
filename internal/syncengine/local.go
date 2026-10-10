package syncengine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
)

func scanLocal(ctx context.Context, root *os.Root, ignored map[string]bool) ([]LocalEntry, error) {
	var entries []LocalEntry
	err := fs.WalkDir(root.FS(), ".", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return errors.New("cannot scan local destination")
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if path == "." {
			return nil
		}
		if err := rootPath(root, path); err != nil {
			return err
		}
		if ignored[path] {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return errors.New("local symlinks are unsupported")
		}
		if d.IsDir() {
			entries = append(entries, LocalEntry{Path: path, Kind: "folder"})
			return nil
		}
		info, err := d.Info()
		if err != nil || !info.Mode().IsRegular() {
			return errors.New("unsupported local file type")
		}
		f, err := root.Open(path)
		if err != nil {
			return errors.New("cannot open local file")
		}
		hash := sha256.New()
		_, readErr := io.Copy(hash, f)
		closeErr := f.Close()
		if readErr != nil || closeErr != nil {
			return errors.New("cannot hash local file")
		}
		entries = append(entries, LocalEntry{Path: path, Kind: "file", Size: info.Size(), SHA256: hex.EncodeToString(hash.Sum(nil))})
		return nil
	})
	return entries, err
}
