package syncengine

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/torfstack/grove/internal/drive"
)

func validateDownloadRemote(expected, current drive.File) error {
	identity := expected.ID != "" && current.ID == expected.ID && expected.Name != "" && current.Name == expected.Name && expected.MIMEType != "" && current.MIMEType == expected.MIMEType && !strings.HasPrefix(expected.MIMEType, "application/vnd.google-apps.")
	parent := len(expected.Parents) == 1 && len(current.Parents) == 1 && expected.Parents[0] != "" && current.Parents[0] == expected.Parents[0]
	content := expected.HasSize && current.HasSize && expected.Size >= 0 && current.Size == expected.Size && validHash(expected.MD5, 16) && validHash(current.MD5, 16) && current.MD5 == expected.MD5
	owned := expected.OwnedByMe && current.OwnedByMe && expected.DriveID == "" && current.DriveID == ""
	active := !expected.Trashed && !current.Trashed && !expected.ExplicitlyTrashed && !current.ExplicitlyTrashed
	capability := expected.CanDownload && current.CanDownload
	revision := current.HeadRevisionID == expected.HeadRevisionID
	modified := current.ModifiedTime == expected.ModifiedTime
	version := expected.Version != "" && current.Version == expected.Version
	if current.Version != expected.Version {
		before, beforeOK := downloadVersion(expected.Version)
		after, afterOK := downloadVersion(current.Version)
		_, timeErr := time.Parse(time.RFC3339Nano, expected.ModifiedTime)
		version = beforeOK && afterOK && after > before && expected.HeadRevisionID != "" && revision && modified && timeErr == nil
	}
	if !identity || !parent || !content || !owned || !active || !capability || !revision || !modified || !version {
		return fmt.Errorf("%w (identity_type=%t parent=%t version=%t revision=%t modified_time=%t content=%t ownership_location=%t active=%t capability=%t)", errPendingRemoteChanged, identity, parent, version, revision, modified, content, owned, active, capability)
	}
	return nil
}

func downloadVersion(value string) (int64, bool) {
	if value == "" {
		return 0, false
	}
	for _, digit := range value {
		if digit < '0' || digit > '9' {
			return 0, false
		}
	}
	number, err := strconv.ParseInt(value, 10, 64)
	return number, err == nil && number > 0
}

func (e *executor) currentDownloadRemote(ctx context.Context, entry Entry) error {
	current, err := e.api.Get(ctx, entry.Remote.ID)
	if err != nil {
		return err
	}
	if err := validateDownloadRemote(entry.Remote, current); err != nil {
		return fmt.Errorf("replacement recovery: %w", err)
	}
	return nil
}
