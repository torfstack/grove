package syncengine

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"slices"
)

func (e *executor) stageDownload(ctx context.Context, entry Entry, path string) (string, error) {
	f, err := e.root.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", errors.New("cannot create exclusive download file")
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	identity, err := fileIdentity(info)
	if err != nil {
		return "", err
	}
	if err = e.parentSync(path); err != nil {
		return "", err
	}
	next := *e.state
	if next.Pending != nil {
		pending := *next.Pending
		pending.TempIdentity = &identity
		pending.Phase = "downloading"
		next.Pending = &pending
	} else {
		journal := *next.Transaction
		journal.TempIdentity = &identity
		next.Transaction = &journal
	}
	if err = e.persist(next); err != nil {
		empty := sha256.Sum256(nil)
		if cleanupErr := e.removeTemp(ctx, path, &identity, hex.EncodeToString(empty[:]), 0, false); cleanupErr != nil {
			return "", errors.Join(err, cleanupErr)
		}
		return "", err
	}
	md5hash := md5.New()
	sha := sha256.New()
	counter := &countWriter{writer: io.MultiWriter(f, md5hash, sha)}
	if err = e.api.Download(ctx, entry.Remote.ID, counter); err != nil {
		return "", err
	}
	if counter.count != entry.Remote.Size || hex.EncodeToString(md5hash.Sum(nil)) != entry.Remote.MD5 {
		return "", errors.New("download checksum or size mismatch")
	}
	fresh, err := e.api.Get(ctx, entry.Remote.ID)
	if err != nil {
		return "", err
	}
	if fresh.Name != entry.Remote.Name || !slices.Equal(fresh.Parents, entry.Remote.Parents) || fresh.ID != entry.Remote.ID || fresh.Version != entry.Remote.Version || fresh.MD5 != entry.Remote.MD5 || fresh.Size != entry.Remote.Size || fresh.Trashed || !fresh.OwnedByMe {
		return "", errors.New("remote file changed during download")
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	if err = f.Sync(); err != nil {
		return "", errors.New("cannot sync downloaded file")
	}
	if err = f.Close(); err != nil {
		return "", errors.New("cannot close downloaded file")
	}
	return hex.EncodeToString(sha.Sum(nil)), nil
}
