package syncengine

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/torfstack/grove/internal/drive"
)

type selectionAPI struct {
	drive.API
	pages map[string]map[string]drive.Page
}

func (a selectionAPI) Get(_ context.Context, id string) (drive.File, error) {
	return drive.File{ID: id, MIMEType: drive.FolderMIME, OwnedByMe: true}, nil
}

func (a selectionAPI) List(_ context.Context, query, token string) (drive.Page, error) {
	return a.pages[query][token], nil
}

func TestDiscoverSelectionFolders(t *testing.T) {
	folder := drive.File{ID: "a", Name: "A", MIMEType: drive.FolderMIME, OwnedByMe: true, Parents: []string{"root"}}
	nested := drive.File{ID: "b", Name: "B", MIMEType: drive.FolderMIME, OwnedByMe: true, Parents: []string{"a"}}
	pages := map[string]map[string]drive.Page{
		drive.ChildrenQuery("root"): {"": {NextToken: "next"}, "next": {Files: []drive.File{folder}}},
		drive.ChildrenQuery("a"):    {"": {Files: []drive.File{nested, {ID: "doc", Name: "Untouched", MIMEType: "application/vnd.google-apps.document"}}}},
	}
	api := selectionAPI{pages: pages}
	folders, err := discoverSelectionFolders(context.Background(), api, "root")
	if err != nil || len(folders) != 2 || folders[1].Path != "A/B" {
		t.Fatalf("folders = %+v, error = %v", folders, err)
	}
	pages[drive.ChildrenQuery("root")]["next"] = drive.Page{Incomplete: true}
	folders, err = discoverSelectionFolders(context.Background(), api, "root")
	if err == nil || len(folders) != 0 {
		t.Fatalf("incomplete listing returned %+v, error = %v", folders, err)
	}
}

func TestScanSelectedFolders(t *testing.T) {
	files := []drive.File{}
	for _, name := range []string{"a", "b", "c", "d", "e", "f", "g"} {
		files = append(files, drive.File{ID: name, Name: name, MIMEType: drive.FolderMIME, OwnedByMe: true, Parents: []string{"root"}})
	}
	api := selectionAPI{pages: map[string]map[string]drive.Page{
		drive.ChildrenQuery("root"): {"": {Files: files}},
		drive.ChildrenQuery("e"):    {"": {Files: []drive.File{{ID: "doc", Name: "Google Doc", MIMEType: "application/vnd.google-apps.document"}}}},
	}}
	snapshot, err := scanSelectedFolders(context.Background(), api, "root", []string{"a", "b", "c", "d"})
	if err != nil || len(snapshot.Entries) != 4 {
		t.Fatalf("scan got %d entries, error = %v", len(snapshot.Entries), err)
	}
	for i, entry := range snapshot.Entries {
		if entry.Remote.ID != files[i].ID {
			t.Fatalf("unexpected selected entry: %q", entry.Path)
		}
	}
	if _, err := scanSelectedFolders(context.Background(), api, "root", []string{"missing"}); err == nil {
		t.Fatal("scan accepted missing folder")
	}
}

func TestScopedLocalScanSkipsExcludedSymlink(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "selected"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("missing", filepath.Join(dir, "excluded")); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	entries, err := scanLocalScope(context.Background(), root, nil, func(path string) bool {
		return within("selected", path)
	})
	if err != nil || len(entries) != 1 || entries[0].Path != "selected" {
		t.Fatalf("entries = %+v, error = %v", entries, err)
	}
}
