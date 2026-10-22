package syncengine

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/torfstack/grove/internal/drive"
)

func completeDownloadFile(f drive.File) drive.File {
	f.HeadRevisionID = "revision-original"
	f.ModifiedTime = "2026-10-22T10:00:00.000Z"
	return f
}

func TestValidateDownloadRemote(t *testing.T) {
	base := file("private-id", "private-name")
	base.Version = "9"
	for _, tc := range []struct {
		name   string
		mutate func(*drive.File)
	}{
		{"id", func(f *drive.File) { f.ID = "other" }},
		{"empty-id", func(f *drive.File) { f.ID = "" }},
		{"name", func(f *drive.File) { f.Name = "other" }},
		{"empty-name", func(f *drive.File) { f.Name = "" }},
		{"mime", func(f *drive.File) { f.MIMEType = "application/octet-stream" }},
		{"empty-mime", func(f *drive.File) { f.MIMEType = "" }},
		{"native", func(f *drive.File) { f.MIMEType = "application/vnd.google-apps.document" }},
		{"parent", func(f *drive.File) { f.Parents = []string{"other"} }},
		{"missing-parent", func(f *drive.File) { f.Parents = nil }},
		{"multiple-parents", func(f *drive.File) { f.Parents = []string{"root", "other"} }},
		{"empty-parent", func(f *drive.File) { f.Parents = []string{""} }},
		{"owner", func(f *drive.File) { f.OwnedByMe = false }},
		{"shared-drive", func(f *drive.File) { f.DriveID = "shared" }},
		{"trash", func(f *drive.File) { f.Trashed = true }},
		{"explicit-trash", func(f *drive.File) { f.ExplicitlyTrashed = true }},
		{"capability", func(f *drive.File) { f.CanDownload = false }},
		{"missing-size", func(f *drive.File) { f.HasSize = false }},
		{"negative-size", func(f *drive.File) { f.Size = -1 }},
		{"size", func(f *drive.File) { f.Size = 1 }},
		{"md5", func(f *drive.File) { f.MD5 = "00000000000000000000000000000000" }},
		{"invalid-md5", func(f *drive.File) { f.MD5 = "invalid" }},
		{"missing-version", func(f *drive.File) { f.Version = "" }},
		{"revision", func(f *drive.File) { f.HeadRevisionID = "other-revision" }},
		{"missing-revision", func(f *drive.File) { f.HeadRevisionID = "" }},
		{"modified-time", func(f *drive.File) { f.ModifiedTime = "2026-10-22T11:00:00Z" }},
		{"missing-time", func(f *drive.File) { f.ModifiedTime = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := base
			tc.mutate(&changed)
			for _, pair := range [][2]drive.File{{base, changed}, {changed, base}} {
				if err := validateDownloadRemote(pair[0], pair[1]); !errors.Is(err, errPendingRemoteChanged) {
					t.Fatalf("invalid evidence accepted: %v", err)
				}
			}
		})
	}
	for _, tc := range []struct {
		name, old, next string
		ok              bool
	}{
		{"equal", "9", "9", true}, {"advance", "9", "10", true},
		{"maximum", "9", "9223372036854775807", true},
		{"regression", "10", "9", false}, {"zero", "0", "10", false},
		{"negative", "-1", "10", false}, {"signed", "9", "+10", false},
		{"whitespace", "9", " 10", false}, {"overflow", "9", "9223372036854775808", false},
		{"missing", "9", "", false}, {"nonnumeric", "9", "later", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expected, current := base, base
			expected.Version, current.Version = tc.old, tc.next
			if err := validateDownloadRemote(expected, current); (err == nil) != tc.ok {
				t.Fatalf("validation error: %v", err)
			}
		})
	}
	for _, field := range []string{"revision", "time", "invalid-time"} {
		for _, advance := range []bool{false, true} {
			expected := base
			switch field {
			case "revision":
				expected.HeadRevisionID = ""
			case "time":
				expected.ModifiedTime = ""
			case "invalid-time":
				expected.ModifiedTime = "invalid"
			}
			current := expected
			if advance {
				current.Version = "10"
			}
			if err := validateDownloadRemote(expected, current); (err == nil) == advance {
				t.Fatalf("fallback %s advance=%t: %v", field, advance, err)
			}
		}
	}
	for _, change := range []func(*drive.File){func(f *drive.File) { f.MIMEType = "application/vnd.google-apps.document" }, func(f *drive.File) { f.HasSize = false }, func(f *drive.File) { f.Parents = []string{""} }, func(f *drive.File) { f.MD5 = "bad" }} {
		invalid := base
		change(&invalid)
		if err := validateDownloadRemote(invalid, invalid); err == nil {
			t.Fatal("matching invalid metadata accepted")
		}
	}
}

func TestDownloadMetadataDiagnostics(t *testing.T) {
	opts, registry := options(t)
	api := downloader()
	api.children[0] = completeDownloadFile(api.children[0])
	api.onDownload = func() { api.children[0].HeadRevisionID = "private-new-revision" }
	_, err := testRun(context.Background(), opts, api, registry)
	if !errors.Is(err, errPendingRemoteChanged) || !strings.Contains(err.Error(), "download verification") || !strings.Contains(err.Error(), "revision=false") {
		t.Fatalf("missing diagnostic categories: %v", err)
	}
	for _, secret := range []string{"private-new-revision", "revision-original", api.children[0].ModifiedTime} {
		if strings.Contains(err.Error(), secret) {
			t.Fatal("raw metadata leaked")
		}
	}
	if _, err := os.Stat(filepath.Join(opts.LocalDir, "file.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("changed revision published")
	}
}

type postDownloadErrorAPI struct{ *downloadAPI }

func (a postDownloadErrorAPI) Get(ctx context.Context, id string) (drive.File, error) {
	if a.downloads > 0 {
		return drive.File{}, os.ErrPermission
	}
	return a.downloadAPI.Get(ctx, id)
}

func TestDownloadMetadataGetFailure(t *testing.T) {
	opts, registry := options(t)
	_, err := testRun(context.Background(), opts, postDownloadErrorAPI{downloader()}, registry)
	if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("read failure lost: %v", err)
	}
	if _, err := os.Stat(filepath.Join(opts.LocalDir, "file.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("unverified download published")
	}
}

func TestDownloadMetadataVersionAdvance(t *testing.T) {
	for _, empty := range []bool{false, true} {
		name := "content"
		if empty {
			name = "empty"
		}
		t.Run(name, func(t *testing.T) {
			opts, registry := options(t)
			api := downloader()
			if empty {
				api.data = nil
				sum := md5.Sum(nil)
				api.children[0].Size = 0
				api.children[0].MD5 = hex.EncodeToString(sum[:])
			}
			api.children[0] = completeDownloadFile(api.children[0])
			api.children[0].Version = "9"
			api.onDownload = func() { api.children[0].Version = "10" }
			result, err := testRun(context.Background(), opts, api, registry)
			if err != nil || result.Downloaded != 1 {
				t.Fatalf("metadata-only advance rejected: %v", err)
			}
			data, err := os.ReadFile(filepath.Join(opts.LocalDir, "file.txt"))
			if err != nil || string(data) != string(api.data) {
				t.Fatalf("wrong downloaded bytes: %v", err)
			}
			canonical, err := canonicalOptions(opts)
			if err != nil {
				t.Fatal(err)
			}
			state, err := loadState(canonical.ProfileDir, Binding{RemoteRoot: canonical.RemoteRoot, LocalDir: canonical.LocalDir, TokenFile: canonical.TokenFile})
			if err != nil || len(state.Completed) != 1 || state.Completed[0].Version != "9" {
				t.Fatalf("planned baseline not preserved: %v", err)
			}
		})
	}
}
