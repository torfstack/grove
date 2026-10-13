package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/torfstack/grove/internal/syncengine"
)

func TestConflictShowAndCandidateResolve(t *testing.T) {
	var gotSide syncengine.ConflictSide
	var gotCandidate int
	services := Services{
		ConflictDetails: func(_ context.Context, _ syncengine.Options, id string) (syncengine.ConflictDetail, error) {
			if id != "safe-id" {
				t.Fatalf("unexpected id %q", id)
			}
			return syncengine.ConflictDetail{ID: id, Path: "folder/file.txt", Candidates: []syncengine.ConflictCandidateView{{Ordinal: 0, Side: syncengine.SideLocal, Size: 4}, {Ordinal: 1, Side: syncengine.SideCloud, ModifiedTime: "2026-01-01T00:00:00Z", Size: 5}, {Ordinal: 2, Side: syncengine.SideCloud, Size: 6}}}, nil
		},
		ResolveConflict: func(_ context.Context, _ syncengine.Options, _ string, side syncengine.ConflictSide, ordinal int) error {
			gotSide, gotCandidate = side, ordinal
			return nil
		},
	}
	root := NewRoot(services)
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"conflicts", "show", "safe-id", "--profile-dir", "p", "--remote-root", "r", "--local-dir", "l", "--token-file", "t"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "0\tlocal") || !strings.Contains(out.String(), "1\tcloud\t\t2026-01-01T00:00:00Z\t5") || strings.Contains(out.String(), "revision_id") || strings.Contains(out.String(), "sha256") || strings.Contains(out.String(), "stage_path") {
		t.Fatalf("unsafe or incomplete show output: %q", out.String())
	}
	root.SetArgs([]string{"conflicts", "resolve", "safe-id", "--candidate", "2", "--profile-dir", "p", "--remote-root", "r", "--local-dir", "l", "--token-file", "t"})
	if err := root.Execute(); err != nil || gotSide != syncengine.SideCloud || gotCandidate != 2 {
		t.Fatalf("candidate resolve side=%q ordinal=%d err=%v", gotSide, gotCandidate, err)
	}
}

func TestDeletionConflictShowDescribesTombstone(t *testing.T) {
	root := NewRoot(Services{ConflictDetails: func(context.Context, syncengine.Options, string) (syncengine.ConflictDetail, error) {
		return syncengine.ConflictDetail{ID: "safe-id", Path: "folder", Candidates: []syncengine.ConflictCandidateView{{Ordinal: 0, Side: syncengine.SideLocal, Description: "deletion"}, {Ordinal: 1, Side: syncengine.SideCloud, Description: "remote tree"}}}, nil
	}})
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"conflicts", "show", "safe-id", "--profile-dir", "p", "--remote-root", "r", "--local-dir", "l", "--token-file", "t"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "-\tlocal\tdeletion") || !strings.Contains(out.String(), "-\tcloud\tremote tree") || strings.Contains(out.String(), "stage_path") || strings.Contains(out.String(), "sha256") {
		t.Fatalf("tombstone detail output is incomplete or unsafe: %q", out.String())
	}
}

func TestConflictResolveRequiresChoiceAndRejectsLocalOrdinal(t *testing.T) {
	called := false
	root := NewRoot(Services{ResolveConflict: func(context.Context, syncengine.Options, string, syncengine.ConflictSide, int) error {
		called = true
		return nil
	}})
	base := []string{"conflicts", "resolve", "id", "--profile-dir", "p", "--remote-root", "r", "--local-dir", "l", "--token-file", "t"}
	for _, extra := range [][]string{nil, {"--candidate", "0"}, {"--candidate", "-1"}, {"--keep", "local", "--candidate", "1"}} {
		root.SetArgs(append(append([]string(nil), base...), extra...))
		if err := root.Execute(); err == nil || called {
			t.Fatalf("invalid resolve choice accepted: %v", extra)
		}
	}
}
