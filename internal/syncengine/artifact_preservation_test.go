package syncengine

import (
	"context"
	"os"
	"testing"
)

func TestRecoveryReplacementPreservesExternalStage(t *testing.T) {
	ex, op, profile := replacement(t)
	save := ex.save
	var stage string
	ex.save = func() error {
		if err := save(); err != nil {
			return err
		}
		if j := ex.state.Transaction; j != nil && j.Phase == "intent" && stage == "" {
			stage = j.TempPath
			return ex.root.WriteFile(stage, []byte("external content"), 0600)
		}
		return nil
	}
	if err := ex.execute(context.Background(), op); err == nil {
		t.Fatal("exclusive creation did not fail")
	}
	s, err := loadState(profile, Binding{})
	if err != nil {
		t.Fatal(err)
	}
	*ex.state = s
	ex.save = save
	recoveryErr := ex.recover(context.Background(), Snapshot{Entries: []Entry{op.Entry}})
	got, readErr := ex.root.ReadFile(stage)
	if recoveryErr == nil || readErr != nil || string(got) != "external content" {
		t.Fatalf("external stage not preserved: recovery=%v read=%v bytes=%q", recoveryErr, readErr, got)
	}
}

func TestRecoveryMoveProbePreservesExternalContent(t *testing.T) {
	ex, _, profile := moving(t)
	ex.state.ProbePath = ".grove-probe-review"
	ex.state.ProbeEntries = []Entry{{Path: "source"}, {Path: "target"}}
	if err := ex.save(); err != nil {
		t.Fatal(err)
	}
	if err := ex.root.Mkdir(ex.state.ProbePath, 0700); err != nil {
		t.Fatal(err)
	}
	path := ex.state.ProbePath + "/source"
	if err := ex.root.WriteFile(path, []byte("external content"), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := loadState(profile, Binding{})
	if err != nil {
		t.Fatal(err)
	}
	*ex.state = s
	recoveryErr := clearProbe(ex.root, ex.state, ex.save)
	got, readErr := ex.root.ReadFile(path)
	if recoveryErr == nil || readErr != nil || string(got) != "external content" {
		t.Fatalf("external probe content not preserved: recovery=%v read=%v bytes=%q", recoveryErr, readErr, got)
	}
}

func TestRecoveryPendingTempCollision(t *testing.T) {
	ex, _, profile := replacement(t)
	old := ex.state.Completed[0]
	old.Path = ".grove-download-existing"
	if err := ex.root.Rename("file.txt", old.Path); err != nil {
		t.Fatal(err)
	}
	ex.state.Completed = []Completed{old}
	entry := Entry{Path: "fresh", Remote: file("fresh", "fresh")}
	ex.state.Pending = &Pending{Entry: entry, TempPath: old.Path, Phase: "downloading"}
	if err := ex.save(); err != nil {
		t.Fatal(err)
	}
	s, err := loadState(profile, Binding{})
	if err == nil {
		*ex.state = s
		recoveryErr := ex.recover(context.Background(), Snapshot{Entries: []Entry{entry}})
		_, statErr := ex.root.Stat(old.Path)
		t.Fatalf("malformed state accepted: recovery=%v tracked-deleted=%v", recoveryErr, os.IsNotExist(statErr))
	}
}

func TestRecoveryPendingPreservesChangedVerifiedStage(t *testing.T) {
	ex, _, profile := replacement(t)
	if err := ex.root.Remove("file.txt"); err != nil {
		t.Fatal(err)
	}
	ex.state.Completed = nil
	api := downloader()
	ex.api = api
	entry := Entry{Path: "file.txt", Remote: api.children[0]}
	save := ex.save
	ex.save = func() error {
		if err := save(); err != nil {
			return err
		}
		if p := ex.state.Pending; p != nil && p.Phase == "verified" {
			return os.ErrPermission
		}
		return nil
	}
	if err := ex.execute(context.Background(), Operation{Kind: OpDownload, Entry: entry}); err == nil {
		t.Fatal("fault not injected")
	}
	s, err := loadState(profile, Binding{})
	if err != nil {
		t.Fatal(err)
	}
	*ex.state = s
	ex.save = save
	stage := s.Pending.TempPath
	if err := ex.root.WriteFile(stage, []byte("external content"), 0600); err != nil {
		t.Fatal(err)
	}
	recoveryErr := ex.recover(context.Background(), Snapshot{Entries: []Entry{entry}})
	got, readErr := ex.root.ReadFile(stage)
	if recoveryErr == nil || readErr != nil || string(got) != "external content" {
		t.Fatalf("changed verified stage not preserved: recovery=%v read=%v bytes=%q", recoveryErr, readErr, got)
	}
}
