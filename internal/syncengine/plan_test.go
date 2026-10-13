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
	return drive.File{ID: id, Name: name, MIMEType: "text/plain", Version: "1", ModifiedTime: "2026-01-01T00:00:00Z", HeadRevisionID: "rev-1", OwnedByMe: true, CanDownload: true, HasSize: true, Size: 0, MD5: "d41d8cd98f00b204e9800998ecf8427e", Parents: []string{"root"}}
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
	return Completed{Path: "a", RemoteID: "a", Version: "1", ModifiedTime: "2026-01-01T00:00:00Z", HeadRevisionID: "rev-1", MD5: "d41d8cd98f00b204e9800998ecf8427e", SHA256: "verified", Kind: "file", Size: 0}
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

func TestPlanIncremental(t *testing.T) {
	old := completed()
	local := []LocalEntry{{Path: "a", Kind: "file", SHA256: "verified"}}
	for _, tc := range []struct {
		name, path, version, md5 string
		want                     []OperationKind
	}{
		{"addition", "b", "1", old.MD5, []OperationKind{OpSkip, OpDownload}},
		{"update", "a", "2", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", []OperationKind{OpReplace}},
		{"metadata", "a", "2", old.MD5, []OperationKind{OpRecord}},
		{"rename", "b", "1", old.MD5, []OperationKind{OpMove, OpSkip}},
		{"rename-update", "b", "2", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", []OperationKind{OpMove, OpReplace}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := file("a", tc.path)
			f.Version = tc.version
			f.MD5 = tc.md5
			remote := Snapshot{Entries: []Entry{{Path: tc.path, Remote: f}}}
			if tc.name == "addition" {
				remote.Entries = []Entry{{Path: "a", Remote: file("a", "a")}, {Path: "b", Remote: file("b", "b")}}
			}
			p, err := BuildPlan(remote, local, State{Completed: []Completed{old}, PopulationComplete: true})
			if err != nil {
				t.Fatal(err)
			}
			kinds := []OperationKind{}
			for _, op := range p.Operations {
				kinds = append(kinds, op.Kind)
			}
			if !reflect.DeepEqual(kinds, tc.want) {
				t.Fatalf("kinds %v want %v", kinds, tc.want)
			}
		})
	}
}
func folderEntry(id, path string) Entry {
	return Entry{Path: path, Remote: drive.File{ID: id, MIMEType: drive.FolderMIME}}
}
func TestPlanNestedMoves(t *testing.T) {
	c := completed()
	c.Path = "old/a"
	state := State{Completed: []Completed{{Path: "old", RemoteID: "folder", Kind: "folder"}, c}, PopulationComplete: true}
	local := []LocalEntry{{Path: "old", Kind: "folder"}, {Path: "old/a", Kind: "file", SHA256: "verified"}}
	remote := Snapshot{Entries: []Entry{folderEntry("folder", "new"), {Path: "new/b", Remote: file("a", "b")}}}
	p, err := BuildPlan(remote, local, state)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Operations) != 3 || p.Operations[0].Kind != OpMove || p.Operations[1].Kind != OpMove || p.Operations[1].Before[0].Path != "new/a" {
		t.Fatalf("wrong nested plan %+v", p)
	}
	remote.Entries[1].Path = "new/a"
	p, err = BuildPlan(remote, local, state)
	if err != nil {
		t.Fatal(err)
	}
	moves := 0
	for _, op := range p.Operations {
		if op.Kind == OpMove {
			moves++
		}
	}
	if moves != 1 {
		t.Fatal("child carried twice")
	}
}
func TestPlanRejectsConflicts(t *testing.T) {
	a, b := completed(), completed()
	b.Path = "b"
	b.RemoteID = "b"
	local := []LocalEntry{{Path: "a", Kind: "file", SHA256: "verified"}, {Path: "b", Kind: "file", SHA256: "verified"}}
	for _, entries := range [][]Entry{
		{{Path: "b", Remote: file("a", "b")}, {Path: "a", Remote: file("b", "a")}},
		{{Path: "c", Remote: file("a", "c")}, {Path: "b", Remote: file("a", "b")}},
		{{Path: "c", Remote: file("a", "c")}},
		{{Path: "c", Remote: file("a", "c")}, {Path: "a", Remote: file("b", "a")}},
	} {
		p, err := BuildPlan(Snapshot{Entries: entries}, local, State{Completed: []Completed{a, b}, PopulationComplete: true})
		if err == nil || len(p.Operations) != 0 {
			t.Fatal("rearrangement accepted")
		}
	}
	changed := file("a", "a")
	changed.Version = "2"
	p, err := BuildPlan(Snapshot{Entries: []Entry{{Path: "a", Remote: changed}}}, []LocalEntry{{Path: "a", Kind: "file", SHA256: "local-edit"}}, State{Completed: []Completed{a}})
	if err == nil || len(p.Operations) != 0 {
		t.Fatal("local and remote conflict accepted")
	}
}

func TestBuildTwoWayPlanUploadsLocalEditWhenRemoteMatchesBaseline(t *testing.T) {
	remote := file("a", "a")
	remote.HeadRevisionID = "rev-1"
	plan, err := BuildTwoWayPlan(
		Snapshot{Entries: []Entry{{Path: "a", Remote: remote}}},
		[]LocalEntry{{Path: "a", Kind: "file", SHA256: "local-edit", Size: 11}},
		State{Completed: []Completed{completed()}, PopulationComplete: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Operations) != 1 || plan.Operations[0].Kind != OpUpload || plan.Operations[0].Local.SHA256 != "local-edit" {
		t.Fatalf("local edit was not planned for upload: %+v", plan.Operations)
	}
}

func TestBuildTwoWayPlanIgnoresVersionOnlyChangeWhenRevisionIsUnchanged(t *testing.T) {
	remote := file("a", "a")
	remote.Version = "2"
	plan, err := BuildTwoWayPlan(
		Snapshot{Entries: []Entry{{Path: "a", Remote: remote}}},
		[]LocalEntry{{Path: "a", Kind: "file", SHA256: "local-edit", Size: 11}},
		State{Completed: []Completed{completed()}, PopulationComplete: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Operations) != 1 || plan.Operations[0].Kind != OpUpload {
		t.Fatalf("version-only metadata change blocked an unchanged-content upload: %+v", plan.Operations)
	}
}

func TestBuildTwoWayPlanAsksBeforeUploadingWhenRemoteChanged(t *testing.T) {
	remote := file("a", "a")
	remote.Version = "2"
	remote.MD5 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	remote.HeadRevisionID = "rev-2"
	plan, err := BuildTwoWayPlan(
		Snapshot{Entries: []Entry{{Path: "a", Remote: remote}}},
		[]LocalEntry{{Path: "a", Kind: "file", SHA256: "local-edit", Size: 11}},
		State{Completed: []Completed{completed()}, PopulationComplete: true, ConflictPolicy: ConflictAsk},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Operations) != 1 || plan.Operations[0].Kind != OpConflict || plan.Operations[0].Conflict == nil {
		t.Fatalf("changed remote did not become a pending conflict: %+v", plan.Operations)
	}
	if plan.Operations[0].Conflict.RemoteVersion != "2" || plan.Operations[0].Local.SHA256 != "local-edit" {
		t.Fatalf("conflict did not retain both candidate fingerprints: %+v", plan.Operations[0])
	}
}

func TestBuildTwoWayPlanAppliesConfiguredConflictPolicy(t *testing.T) {
	remote := file("a", "a")
	remote.Version = "2"
	remote.MD5 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	remote.HeadRevisionID = "rev-2"
	input := Snapshot{Entries: []Entry{{Path: "a", Remote: remote}}}
	local := []LocalEntry{{Path: "a", Kind: "file", SHA256: "local-edit", Size: 11}}
	for _, tc := range []struct {
		policy ConflictPolicy
		kind   OperationKind
		side   ConflictSide
	}{
		{ConflictKeepLocal, OpConflict, SideLocal},
		{ConflictKeepCloud, OpConflict, SideCloud},
	} {
		plan, err := BuildTwoWayPlan(input, local, State{Completed: []Completed{completed()}, PopulationComplete: true, ConflictPolicy: tc.policy})
		if err != nil {
			t.Fatal(err)
		}
		if len(plan.Operations) != 1 || plan.Operations[0].Kind != tc.kind || plan.Operations[0].ResolutionSide != tc.side {
			t.Fatalf("policy %q produced %+v", tc.policy, plan.Operations)
		}
	}
}

func TestBuildTwoWayPlanCreatesUntrackedLocalEntries(t *testing.T) {
	plan, err := BuildTwoWayPlan(Snapshot{}, []LocalEntry{
		{Path: "new", Kind: "folder"},
		{Path: "new/file.txt", Kind: "file", SHA256: "new-content", Size: 12},
	}, State{PopulationComplete: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Operations) != 2 || plan.Operations[0].Kind != OpCreate || plan.Operations[0].Local.Path != "new" || plan.Operations[1].Kind != OpCreate || plan.Operations[1].Local.Path != "new/file.txt" {
		t.Fatalf("local folder and child were not planned in parent order: %+v", plan.Operations)
	}
}

func TestBuildTwoWayPlanRejectsUnknownConflictPolicy(t *testing.T) {
	_, err := BuildTwoWayPlan(Snapshot{}, nil, State{ConflictPolicy: "overwrite"})
	if err == nil {
		t.Fatal("unknown conflict policy was accepted")
	}
}

func TestBuildTwoWayPlanConflictsOnLocalAndRemoteSamePathAddition(t *testing.T) {
	remote := Snapshot{Entries: []Entry{{Path: "same.txt", Remote: file("remote", "same.txt")}}}
	local := []LocalEntry{{Path: "same.txt", Kind: "file", SHA256: "local-content", Size: 12}}
	for _, tc := range []struct {
		policy ConflictPolicy
		want   ConflictSide
	}{{ConflictAsk, ""}, {ConflictKeepLocal, ""}, {ConflictKeepCloud, SideCloud}} {
		plan, err := BuildTwoWayPlan(remote, local, State{PopulationComplete: true, ConflictPolicy: tc.policy})
		if err != nil || len(plan.Operations) != 1 || plan.Operations[0].Kind != OpConflict || plan.Operations[0].Conflict == nil {
			t.Fatalf("same-path independent additions did not become a conflict: %v %+v", err, plan.Operations)
		}
		if plan.Operations[0].Conflict.Baseline != (Completed{}) || plan.Operations[0].ResolutionSide != tc.want {
			t.Fatalf("policy %q selected an unsupported side for a baseline-free conflict: %+v", tc.policy, plan.Operations[0])
		}
		pending := *plan.Operations[0].Conflict
		pending.SelectedSide = SideLocal
		repeated, err := BuildTwoWayPlan(remote, local, State{PopulationComplete: true, ConflictPolicy: tc.policy, Conflicts: []Conflict{pending}})
		if err != nil || len(repeated.Operations) != 1 || repeated.Operations[0].ResolutionSide != tc.want {
			t.Fatalf("saved unsupported local selection was reused: plan=%+v err=%v", repeated, err)
		}
	}
}

func TestBuildTwoWayPlanRetainsAskConflictAfterMetadataChange(t *testing.T) {
	remote := file("a", "a")
	remote.Version, remote.HeadRevisionID = "2", "rev-2"
	remote.MD5 = md5Text("cloud")
	remote.Size = 5
	local := []LocalEntry{{Path: "a", Kind: "file", SHA256: hashText("local"), Size: 5}}
	state := State{Completed: []Completed{completed()}, PopulationComplete: true, ConflictPolicy: ConflictAsk}
	plan, err := BuildTwoWayPlan(Snapshot{Entries: []Entry{{Path: "a", Remote: remote}}}, local, state)
	if err != nil || len(plan.Operations) != 1 || plan.Operations[0].Conflict == nil {
		t.Fatalf("initial conflict: %+v, %v", plan, err)
	}
	pending := *plan.Operations[0].Conflict
	state.Conflicts = []Conflict{pending}
	remote.Version = "3"
	plan, err = BuildTwoWayPlan(Snapshot{Entries: []Entry{{Path: "a", Remote: remote}}}, local, state)
	if err != nil || len(plan.Operations) != 1 || plan.Operations[0].Conflict == nil {
		t.Fatalf("retried conflict: %+v, %v", plan, err)
	}
	if plan.Operations[0].Conflict.ID != pending.ID || plan.Operations[0].ResolutionSide != "" {
		t.Fatal("metadata-only retry replaced the unresolved conflict identity")
	}
	remote.HeadRevisionID = "rev-3"
	plan, err = BuildTwoWayPlan(Snapshot{Entries: []Entry{{Path: "a", Remote: remote}}}, local, state)
	if err != nil || len(plan.Operations) != 1 || plan.Operations[0].Conflict == nil || plan.Operations[0].Conflict.ID == pending.ID {
		t.Fatalf("new content revision reused the pending identity: %+v, %v", plan, err)
	}
}

func TestBuildTwoWayPlanTrashMissingTrackedFile(t *testing.T) {
	base := completed()
	plan, err := BuildTwoWayPlan(
		Snapshot{Entries: []Entry{{Path: "a", Remote: file("a", "a")}}},
		nil,
		State{Completed: []Completed{base}, PopulationComplete: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Operations) != 1 || plan.Operations[0].Kind != OperationKind("trash") || plan.Operations[0].Entry.Remote.ID != "a" {
		t.Fatalf("missing tracked file did not plan its Drive item for trash: %+v", plan.Operations)
	}
	if len(plan.Operations[0].Before) != 1 || plan.Operations[0].Before[0] != base {
		t.Fatalf("trash operation lost the saved baseline: %+v", plan.Operations[0])
	}
}

func TestBuildTwoWayPlanTrashesMissingEmptyFolder(t *testing.T) {
	base := Completed{Path: "empty", RemoteID: "empty-id", Version: "7", ModifiedTime: "2026-01-01T00:00:00Z", Kind: "folder"}
	remote := drive.File{ID: base.RemoteID, Name: "empty", MIMEType: drive.FolderMIME, Version: base.Version, ModifiedTime: base.ModifiedTime, OwnedByMe: true, Parents: []string{"root"}}
	plan, err := BuildTwoWayPlan(Snapshot{Entries: []Entry{{Path: base.Path, Remote: remote}}}, nil, State{Completed: []Completed{base}, PopulationComplete: true, Binding: Binding{RemoteRoot: "root"}})
	if err != nil || len(plan.Operations) != 1 || plan.Operations[0].Kind != OpTrash || len(plan.Operations[0].Before) != 1 {
		t.Fatalf("missing empty folder did not produce one trash operation: %+v %v", plan, err)
	}
}

func TestBuildTwoWayPlanCollapsesMissingFolderSubtree(t *testing.T) {
	folder := Completed{Path: "a", RemoteID: "folder-a", Version: "1", ModifiedTime: "2026-01-01T00:00:00Z", Kind: "folder"}
	childFolder := Completed{Path: "a/b", RemoteID: "folder-b", Version: "1", ModifiedTime: "2026-01-01T00:00:00Z", Kind: "folder"}
	childFile := completed()
	childFile.Path, childFile.RemoteID = "a/b/file", "file"
	leaf := file("file", "file")
	leaf.Parents = []string{"folder-b"}
	remote := Snapshot{Entries: []Entry{
		{Path: "a", Remote: drive.File{ID: "folder-a", Name: "a", MIMEType: drive.FolderMIME, Version: "1", ModifiedTime: "2026-01-01T00:00:00Z", OwnedByMe: true, Parents: []string{"root"}}},
		{Path: "a/b", Remote: drive.File{ID: "folder-b", Name: "b", MIMEType: drive.FolderMIME, Version: "1", ModifiedTime: "2026-01-01T00:00:00Z", OwnedByMe: true, Parents: []string{"folder-a"}}},
		{Path: "a/b/file", Remote: leaf},
	}}
	plan, err := BuildTwoWayPlan(remote, nil, State{Completed: []Completed{childFile, childFolder, folder}, PopulationComplete: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Operations) != 1 || plan.Operations[0].Kind != OperationKind("trash") || plan.Operations[0].Entry.Remote.ID != "folder-a" {
		t.Fatalf("missing folder subtree was not collapsed to one trash: %+v", plan.Operations)
	}
	want := []string{"a", "a/b", "a/b/file"}
	if len(plan.Operations[0].Before) != len(want) {
		t.Fatalf("trash operation has incomplete subtree baseline: %+v", plan.Operations[0].Before)
	}
	for i, path := range want {
		if plan.Operations[0].Before[i].Path != path {
			t.Fatalf("subtree baseline order = %+v, want %v", plan.Operations[0].Before, want)
		}
	}
}

func TestBuildTwoWayPlanSurfacesChangedRemoteAsDeletionConflict(t *testing.T) {
	base := completed()
	remote := file("a", "a")
	remote.Version, remote.HeadRevisionID = "2", "rev-2"
	remote.MD5 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	for _, policy := range []ConflictPolicy{ConflictAsk, ConflictKeepLocal, ConflictKeepCloud} {
		plan, err := BuildTwoWayPlan(
			Snapshot{Entries: []Entry{{Path: "a", Remote: remote}}},
			nil,
			State{Completed: []Completed{base}, PopulationComplete: true, ConflictPolicy: policy},
		)
		if err != nil {
			t.Fatalf("policy %q: %v", policy, err)
		}
		if len(plan.Operations) != 1 || plan.Operations[0].Kind != OpConflict || plan.Operations[0].ResolutionSide != "" || plan.Operations[0].Conflict == nil || !plan.Operations[0].Conflict.Deletion {
			t.Fatalf("policy %q selected or lost a deletion conflict: %+v", policy, plan.Operations)
		}
	}
}

func TestBuildTwoWayPlanDoesNotAutoTrashAnExistingDeletionConflict(t *testing.T) {
	base := completed()
	changed := file("a", "a")
	changed.Version, changed.HeadRevisionID = "2", "rev-2"
	changed.MD5 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	first, err := BuildTwoWayPlan(Snapshot{Entries: []Entry{{Path: "a", Remote: changed}}}, nil, State{Completed: []Completed{base}, PopulationComplete: true})
	if err != nil || len(first.Operations) != 1 || first.Operations[0].Conflict == nil {
		t.Fatalf("failed to create initial tombstone: %+v %v", first, err)
	}
	pending := *first.Operations[0].Conflict
	unchanged := file("a", "a")
	plan, err := BuildTwoWayPlan(Snapshot{Entries: []Entry{{Path: "a", Remote: unchanged}}}, nil, State{Completed: []Completed{base}, Conflicts: []Conflict{pending}, PopulationComplete: true, ConflictPolicy: ConflictKeepLocal})
	if err != nil || len(plan.Operations) != 1 || plan.Operations[0].Kind != OpConflict || plan.Operations[0].ResolutionSide != "" || plan.Operations[0].Conflict == nil || !plan.Operations[0].Conflict.Deletion {
		t.Fatalf("existing tombstone was auto-resolved after remote changed back: %+v %v", plan, err)
	}
}

func TestBuildTwoWayPlanPreservesSubtreeConflictForNewRemoteDescendant(t *testing.T) {
	folder := Completed{Path: "a", RemoteID: "folder-a", Version: "1", Kind: "folder"}
	remote := Snapshot{Entries: []Entry{
		{Path: "a", Remote: drive.File{ID: "folder-a", Name: "a", MIMEType: drive.FolderMIME, Version: "1", OwnedByMe: true, Parents: []string{"root"}}},
		{Path: "a/new", Remote: file("new", "new")},
	}}
	plan, err := BuildTwoWayPlan(remote, nil, State{Completed: []Completed{folder}, PopulationComplete: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Operations) != 1 || plan.Operations[0].Kind != OpConflict || plan.Operations[0].Conflict == nil || !plan.Operations[0].Conflict.Deletion {
		t.Fatalf("remote descendant was not preserved behind a deletion conflict: %+v", plan.Operations)
	}
}

func TestBuildTwoWayPlanConflictsWhenDeletedRemoteItemMoved(t *testing.T) {
	base := completed()
	moved := file("a", "b")
	moved.HeadRevisionID = "rev-2"
	plan, err := BuildTwoWayPlan(Snapshot{Entries: []Entry{{Path: "b", Remote: moved}}}, nil, State{Completed: []Completed{base}, PopulationComplete: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Operations) != 1 || plan.Operations[0].Kind != OpConflict || plan.Operations[0].Conflict == nil || !plan.Operations[0].Conflict.Deletion || plan.Operations[0].Conflict.Path != "a" || plan.Operations[0].Entry.Path != "b" {
		t.Fatalf("moved remote item did not remain behind a deletion conflict: %+v", plan.Operations)
	}
}

func TestBuildTwoWayPlanConflictsWhenFolderMetadataChanged(t *testing.T) {
	folder := Completed{Path: "a", RemoteID: "folder-a", Version: "1", ModifiedTime: "2026-01-01T00:00:00Z", Kind: "folder"}
	remoteFolder := drive.File{ID: "folder-a", Name: "a", MIMEType: drive.FolderMIME, Version: "2", ModifiedTime: "2026-01-02T00:00:00Z", OwnedByMe: true, Parents: []string{"root"}}
	plan, err := BuildTwoWayPlan(Snapshot{Entries: []Entry{{Path: "a", Remote: remoteFolder}}}, nil, State{Completed: []Completed{folder}, PopulationComplete: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Operations) != 1 || plan.Operations[0].Kind != OpConflict || plan.Operations[0].Conflict == nil || !plan.Operations[0].Conflict.Deletion {
		t.Fatalf("changed folder metadata did not become a deletion conflict: %+v", plan.Operations)
	}
}

func TestBuildTwoWayPlanRecordsDeletedFileTypeChangeAsTombstone(t *testing.T) {
	base := completed()
	entry := Entry{Path: "a", Remote: drive.File{ID: base.RemoteID, Name: "a", MIMEType: drive.FolderMIME, Version: "2", ModifiedTime: base.ModifiedTime, OwnedByMe: true, Parents: []string{"root"}}}
	state := State{Version: 3, Completed: []Completed{base}, PopulationComplete: true}
	plan, err := BuildTwoWayPlan(Snapshot{Entries: []Entry{entry}}, nil, state)
	if err != nil || len(plan.Operations) != 1 || plan.Operations[0].Kind != OpConflict || plan.Operations[0].Conflict == nil {
		t.Fatalf("changed type did not become a deletion conflict: %+v %v", plan, err)
	}
	c := *plan.Operations[0].Conflict
	if err = validateConflicts([]Conflict{c}, state.Completed, true); err != nil {
		t.Fatalf("changed-type tombstone cannot persist: %v", err)
	}
}

func TestBuildTwoWayPlanKeepsRemoteRemovalUnsupported(t *testing.T) {
	_, err := BuildTwoWayPlan(Snapshot{}, nil, State{Completed: []Completed{completed()}, PopulationComplete: true})
	if err == nil {
		t.Fatal("complete remote listing without tracked ID was treated as local deletion")
	}
}

func TestBuildPlanStillRejectsMissingTrackedLocalFile(t *testing.T) {
	_, err := BuildPlan(
		Snapshot{Entries: []Entry{{Path: "a", Remote: file("a", "a")}}},
		nil,
		State{Completed: []Completed{completed()}, PopulationComplete: true},
	)
	if err == nil {
		t.Fatal("download-only planner accepted a missing tracked file")
	}
}

func TestBuildTwoWayPlanDoesNotAutoResolveMultiCandidateConflict(t *testing.T) {
	remote := file("a", "a")
	remote.Version, remote.HeadRevisionID = "2", "rev-2"
	remote.MD5, remote.Size = md5Text("cloud"), 5
	base := completed()
	pending := Conflict{Path: "a", Baseline: base, LocalSHA256: hashText("local"), LocalSize: 5,
		RemoteID: remote.ID, RemoteVersion: remote.Version, RemoteRevisionID: remote.HeadRevisionID, RemoteMD5: remote.MD5, RemoteSize: remote.Size,
		CandidateSetVersion: 1, CloudCandidates: []CloudCandidate{{RevisionID: "rev-1"}, {RevisionID: "rev-2"}}, SelectedSide: ""}
	pending.ID = conflictID(pending)
	plan, err := BuildTwoWayPlan(Snapshot{Entries: []Entry{{Path: "a", Remote: remote}}}, []LocalEntry{{Path: "a", Kind: "file", SHA256: hashText("local"), Size: 5}}, State{
		Completed: []Completed{base}, PopulationComplete: true, ConflictPolicy: ConflictKeepCloud, Conflicts: []Conflict{pending},
	})
	if err != nil || len(plan.Operations) != 1 || plan.Operations[0].Kind != OpConflict || plan.Operations[0].ResolutionSide != "" {
		t.Fatalf("multi-candidate conflict was auto-resolved: plan=%+v err=%v", plan, err)
	}
}
