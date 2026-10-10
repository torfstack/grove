package syncengine

import (
	"context"
	"errors"
	"github.com/torfstack/grove/internal/drive"
	"io"
	"reflect"
	"testing"
)

type treeAPI struct {
	root       drive.File
	children   []drive.File
	incomplete bool
}

func (a treeAPI) Get(context.Context, string) (drive.File, error) { return a.root, nil }
func (a treeAPI) List(context.Context, string, string) (drive.Page, error) {
	return drive.Page{Files: a.children, Incomplete: a.incomplete}, nil
}
func (treeAPI) Download(context.Context, string, io.Writer) error {
	return errors.New("unexpected download")
}
func (treeAPI) Create(context.Context, drive.Create, io.Reader) (drive.File, error) {
	return drive.File{}, errors.New("unexpected create")
}
func (treeAPI) Trash(context.Context, string) error { return errors.New("unexpected trash") }
func file(id, name string) drive.File {
	return drive.File{ID: id, Name: name, MIMEType: "text/plain", Version: "1", OwnedByMe: true, CanDownload: true, HasSize: true, Size: 0, MD5: "d41d8cd98f00b204e9800998ecf8427e", Parents: []string{"root"}}
}
func tree() treeAPI {
	return treeAPI{root: drive.File{ID: "root", Name: "root", MIMEType: drive.FolderMIME, OwnedByMe: true}, children: []drive.File{file("a", "a"), file("b", "b")}}
}
func TestScanSupportedTree(t *testing.T) {
	s, err := Scan(context.Background(), tree(), "root")
	if err != nil || len(s.Entries) != 2 {
		t.Fatal("scan failed", err)
	}
}
func TestScanIncompleteNoPlan(t *testing.T) {
	a := tree()
	a.incomplete = true
	if _, err := Scan(context.Background(), a, "root"); err == nil {
		t.Fatal("accepted incomplete tree")
	}
}
func TestScanRejectsUnsupported(t *testing.T) {
	for _, change := range []func(*drive.File){func(f *drive.File) { f.MIMEType = "application/vnd.google-apps.document" }, func(f *drive.File) { f.OwnedByMe = false }, func(f *drive.File) { f.HasSize = false }, func(f *drive.File) { f.MD5 = "" }, func(f *drive.File) { f.CanDownload = false }, func(f *drive.File) { f.DriveID = "shared" }} {
		a := tree()
		change(&a.children[0])
		if _, err := Scan(context.Background(), a, "root"); err == nil {
			t.Fatal("unsupported entry accepted")
		}
	}
}
func TestScanUnsafeNames(t *testing.T) {
	for _, name := range []string{"", ".", "..", "a/b", "a\\b", "a\x00b", "b"} {
		a := tree()
		a.children[0].Name = name
		if _, err := Scan(context.Background(), a, "root"); err == nil {
			t.Fatalf("unsafe name accepted %q", name)
		}
	}
}
func TestPlanStableOrder(t *testing.T) {
	a := Snapshot{Entries: []Entry{{Path: "b", Remote: file("b", "b")}, {Path: "a", Remote: file("a", "a")}}}
	b := Snapshot{Entries: []Entry{a.Entries[1], a.Entries[0]}}
	one, err := BuildPlan(a, nil, State{})
	if err != nil {
		t.Fatal(err)
	}
	two, err := BuildPlan(b, nil, State{})
	if err != nil || !reflect.DeepEqual(one, two) || one.Operations[0].Entry.Path != "a" {
		t.Fatal("unstable plan")
	}
}
func completed() Completed {
	return Completed{Path: "a", RemoteID: "a", Version: "1", MD5: "d41d8cd98f00b204e9800998ecf8427e", SHA256: "verified", Kind: "file", Size: 0}
}
func TestPlanResume(t *testing.T) {
	a := Snapshot{Entries: []Entry{{Path: "a", Remote: file("a", "a")}, {Path: "b", Remote: file("b", "b")}}}
	p, err := BuildPlan(a, []LocalEntry{{Path: "a", Kind: "file", SHA256: "verified"}}, State{Completed: []Completed{completed()}})
	if err != nil || p.Operations[0].Kind != "skip" || p.Operations[1].Kind != "download" {
		t.Fatal("bad resume", err)
	}
}
func TestPlanNoOp(t *testing.T) {
	p, err := BuildPlan(Snapshot{Entries: []Entry{{Path: "a", Remote: file("a", "a")}}}, []LocalEntry{{Path: "a", Kind: "file", SHA256: "verified"}}, State{Completed: []Completed{completed()}, PopulationComplete: true})
	if err != nil || len(p.Operations) != 1 || p.Operations[0].Kind != "skip" {
		t.Fatal("no-op failed", err)
	}
}
func TestPlanConflicts(t *testing.T) {
	remote := Snapshot{Entries: []Entry{{Path: "a", Remote: file("a", "a")}}}
	for _, tc := range []struct {
		r Snapshot
		l []LocalEntry
		s State
	}{{remote, []LocalEntry{{Path: "a", Kind: "file", SHA256: "changed"}}, State{Completed: []Completed{completed()}}}, {Snapshot{}, nil, State{Completed: []Completed{completed()}}}, {remote, []LocalEntry{{Path: "unexpected", Kind: "file"}}, State{}}, {remote, nil, State{Completed: []Completed{completed()}}}} {
		if _, err := BuildPlan(tc.r, tc.l, tc.s); err == nil {
			t.Fatal("conflict accepted")
		}
	}
}
