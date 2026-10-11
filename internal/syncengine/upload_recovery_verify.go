package syncengine

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"io"

	"github.com/torfstack/grove/internal/drive"
)

func recoveredStructure(j Journal, f drive.File) bool {
	expected := j.Operation.Entry.Remote
	if f.ID != expected.ID || f.Name != expected.Name || f.MIMEType != expected.MIMEType || !f.OwnedByMe || f.Trashed || f.DriveID != "" || len(f.Parents) != 1 || len(expected.Parents) != 1 || f.Parents[0] != expected.Parents[0] {
		return false
	}
	return j.Operation.Kind != OpCreate || f.Properties["grove_operation"] == j.OperationMarker
}

func (e *executor) verifyRecoveredRemote(ctx context.Context, j Journal, current drive.File) (Completed, error) {
	if !recoveredStructure(j, current) {
		return Completed{}, blockedRecovery("remote-changed")
	}
	result := Completed{Path: j.Operation.Entry.Path, RemoteID: current.ID, Version: current.Version, ModifiedTime: current.ModifiedTime, HeadRevisionID: current.HeadRevisionID, Kind: j.Operation.Local.Kind}
	if result.Kind == "file" {
		if !current.HasSize || !current.CanDownload || current.Version == "" || current.HeadRevisionID == "" || current.ModifiedTime == "" || current.Size != j.StageSize || current.MD5 != j.StageMD5 {
			return Completed{}, blockedRecovery("remote-changed")
		}
		sha, md5sum := sha256.New(), md5.New()
		count := &countWriter{writer: io.MultiWriter(sha, md5sum)}
		if err := e.api.Download(ctx, current.ID, &contextWriter{ctx: ctx, writer: count}); err != nil {
			return Completed{}, err
		}
		if count.count != j.StageSize || hex.EncodeToString(sha.Sum(nil)) != j.StageSHA256 || hex.EncodeToString(md5sum.Sum(nil)) != j.StageMD5 {
			return Completed{}, blockedRecovery("remote-changed")
		}
		result.Size, result.MD5, result.SHA256 = j.StageSize, j.StageMD5, j.StageSHA256
	}
	after, err := e.api.Get(ctx, current.ID)
	if err != nil {
		return Completed{}, err
	}
	if !recoveredStructure(j, after) || after.HeadRevisionID != current.HeadRevisionID || after.ModifiedTime != current.ModifiedTime || after.Size != current.Size || after.MD5 != current.MD5 {
		return Completed{}, blockedRecovery("remote-changed")
	}
	if err = ctx.Err(); err != nil {
		return Completed{}, err
	}
	result.Version = after.Version
	return result, nil
}

type contextWriter struct {
	ctx    context.Context
	writer io.Writer
}

func (w *contextWriter) Write(p []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	return w.writer.Write(p)
}
