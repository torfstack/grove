package syncengine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/torfstack/grove/internal/privatefs"
)

func TestExportRecoveryOfflineCopiesVerifiedStage(t *testing.T) {
	for _, candidate := range []ConflictSide{SideLocal, SideCloud} {
		for _, data := range []string{"", "edit", strings.Repeat("stream", 200000)} {
			t.Run(string(candidate)+fmt.Sprint(len(data)), func(t *testing.T) {
				s, o, state, _, _ := recoveryFixture(t, OpUpload)
				j := state.Transaction
				path, id := createConflictTestStage(t, o.ProfileDir, ".grove-upload-export", []byte(data))
				j.StagePath, j.StageIdentity, j.StageSHA256, j.StageMD5, j.StageSize = path, &id, hashText(data), md5Hex([]byte(data)), int64(len(data))
				j.Operation.Local.SHA256, j.Operation.Local.Size = j.StageSHA256, j.StageSize
				if candidate == SideCloud {
					path, id = createConflictTestStage(t, o.ProfileDir, ".grove-cloud-conflict-export", []byte(data))
					j.CloudStagePath, j.CloudStageIdentity, j.CloudSHA256, j.CloudMD5, j.CloudSize, j.CloudRevisionID = path, &id, hashText(data), md5Hex([]byte(data)), int64(len(data)), "racer"
					j.Phase = "conflict-captured"
					j.PinnedRevisionID = j.PreflightRevisionID
					j.After = []Completed{baselineFile("a", "a", []byte(data))}
				}
				if err := saveState(o.ProfileDir, *state); err != nil {
					t.Fatal(err)
				}
				before, _ := os.ReadFile(filepath.Join(o.ProfileDir, "state.json"))
				output := filepath.Join(t.TempDir(), "saved")
				if err := s.ExportRecovery(context.Background(), o, RecoveryExportOptions{Candidate: candidate, Output: output}); err != nil {
					t.Fatal(err)
				}
				bytes, err := os.ReadFile(output)
				if err != nil || string(bytes) != data {
					t.Fatal("wrong exported bytes")
				}
				info, err := os.Stat(output)
				if err != nil || info.Mode().Perm() != 0600 {
					t.Fatal("nonprivate export")
				}
				after, _ := os.ReadFile(filepath.Join(o.ProfileDir, "state.json"))
				if string(before) != string(after) {
					t.Fatal("export modified profile")
				}
				if err = verifyPrivateStage(o.ProfileDir, path, &id, hashText(data), md5Hex([]byte(data)), int64(len(data))); err != nil {
					t.Fatal("export removed source", err)
				}
			})
		}
	}
}

func TestExportRecoveryRejectsUnsafeOutput(t *testing.T) {
	for _, mode := range []string{"existing", "symlink", "missing-parent", "profile", "local", "registered", "other-profile", "alias", "malformed-registry", "partial", "changed", "folder", "invalid-candidate"} {
		t.Run(mode, func(t *testing.T) {
			s, o, state, _, _ := recoveryFixture(t, OpUpload)
			output := filepath.Join(t.TempDir(), "saved")
			candidate := SideLocal
			switch mode {
			case "existing":
				if err := os.WriteFile(output, []byte("existing"), 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink(filepath.Join(o.LocalDir, "a"), output); err != nil {
					t.Fatal(err)
				}
			case "missing-parent":
				output = filepath.Join(t.TempDir(), "absent", "saved")
			case "profile":
				output = filepath.Join(o.ProfileDir, "saved")
			case "local":
				output = filepath.Join(o.LocalDir, "saved")
			case "registered", "other-profile", "alias":
				otherDest, otherProfile := t.TempDir(), t.TempDir()
				lease, err := registerDestination(s.RegistryDir, otherProfile, otherDest)
				if err != nil {
					t.Fatal(err)
				}
				if err = lease.Close(); err != nil {
					t.Fatal(err)
				}
				output = filepath.Join(otherDest, "saved")
				if mode == "other-profile" {
					output = filepath.Join(otherProfile, "saved")
				}
				if mode == "alias" {
					link := filepath.Join(t.TempDir(), "alias")
					if err = os.Symlink(otherDest, link); err != nil {
						t.Fatal(err)
					}
					output = filepath.Join(link, "saved")
				}
			case "malformed-registry":
				if err := os.MkdirAll(s.RegistryDir, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(s.RegistryDir, "registry.json"), []byte(`{"version":1,"entries":[{"profile":"","destination":""}]}`), 0600); err != nil {
					t.Fatal(err)
				}
			case "partial":
				candidate = SideCloud
				path, id := createConflictTestStage(t, o.ProfileDir, ".grove-cloud-conflict-partial", []byte("partial"))
				j := state.Transaction
				j.CloudStagePath, j.CloudStageIdentity, j.CloudRevisionID, j.CloudMD5, j.CloudSize = path, &id, "racer", md5Hex([]byte("cloud")), 5
				j.Phase = "capture-intent"
				j.PinnedRevisionID = j.PreflightRevisionID
				j.After = []Completed{baselineFile("a", "a", []byte("edit"))}
				if err := saveState(o.ProfileDir, *state); err != nil {
					t.Fatal(err)
				}
			case "changed":
				if err := os.WriteFile(filepath.Join(o.ProfileDir, state.Transaction.StagePath), []byte("evil"), 0600); err != nil {
					t.Fatal(err)
				}
			case "folder":
				state.Transaction.Operation.Local.Kind = "folder"
				if err := saveState(o.ProfileDir, *state); err != nil {
					t.Fatal(err)
				}
			case "invalid-candidate":
				candidate = "other"
			}
			before, _ := os.ReadFile(filepath.Join(o.ProfileDir, "state.json"))
			if err := s.ExportRecovery(context.Background(), o, RecoveryExportOptions{Candidate: candidate, Output: output}); err == nil {
				t.Fatal("unsafe export accepted")
			}
			after, _ := os.ReadFile(filepath.Join(o.ProfileDir, "state.json"))
			if string(before) != string(after) {
				t.Fatal("state changed")
			}
			if mode == "existing" {
				data, _ := os.ReadFile(output)
				if string(data) != "existing" {
					t.Fatal("existing output overwritten")
				}
			}
		})
	}
}

func TestExportPublicationRaces(t *testing.T) {
	for _, mode := range []string{"source-replaced", "parent-renamed", "target-created", "cancel", "read", "write", "sync-file", "link", "sync-parent", "close", "temp-replaced"} {
		t.Run(mode, func(t *testing.T) {
			s, o, state, _, _ := recoveryFixture(t, OpUpload)
			parent := t.TempDir()
			output := filepath.Join(parent, "saved")
			d, err := acquireExportDestination(s.RegistryDir, o.ProfileDir, o.LocalDir, output)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = d.Close() }()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ops := defaultRecoveryExportIO()
			originalCopy := ops.copy
			originalClose := ops.close
			sentinel := errors.New("io failure")
			ops.copy = func(w io.Writer, r io.Reader) (int64, error) {
				switch mode {
				case "source-replaced":
					p := filepath.Join(o.ProfileDir, state.Transaction.StagePath)
					if err := os.Rename(p, p+".original"); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(p, []byte("edit"), 0600); err != nil {
						t.Fatal(err)
					}
				case "parent-renamed":
					if err := os.Rename(parent, parent+"-moved"); err != nil {
						t.Fatal(err)
					}
					if err := os.Mkdir(parent, 0700); err != nil {
						t.Fatal(err)
					}
				case "target-created":
					if err := os.WriteFile(output, []byte("racer"), 0600); err != nil {
						t.Fatal(err)
					}
				case "cancel":
					cancel()
				case "read":
					return 0, sentinel
				}
				return originalCopy(w, r)
			}
			switch mode {
			case "write":
				ops.copy = func(w io.Writer, r io.Reader) (int64, error) {
					if _, err := w.Write([]byte("par")); err != nil {
						return 0, err
					}
					return 3, sentinel
				}
			case "sync-file":
				ops.syncFile = func(*os.File) error { return sentinel }
			case "link":
				ops.link = func(*os.Root, string, string) error { return sentinel }
			case "sync-parent":
				ops.syncParent = func(*os.Root) error { return sentinel }
			case "close":
				ops.close = func(f *os.File) error { _ = originalClose(f); return sentinel }
			case "temp-replaced":
				ops.syncFile = func(f *os.File) error {
					path := filepath.Join(parent, filepath.Base(f.Name()))
					if err := os.Rename(path, path+".original"); err != nil {
						t.Fatal(err)
					}
					return os.WriteFile(path, []byte("intruder"), 0600)
				}
			}
			published, err := exportRecoveryStage(ctx, o.ProfileDir, *state.Transaction, SideLocal, d, ops)
			if err == nil {
				t.Fatal("fault accepted")
			}
			if mode == "sync-parent" {
				if !published {
					t.Fatal("lost published result")
				}
			} else if published {
				t.Fatal("published invalid bytes")
			}
			if mode == "target-created" {
				data, _ := os.ReadFile(output)
				if string(data) != "racer" {
					t.Fatal("racing target overwritten")
				}
			}
			if mode == "temp-replaced" {
				files, _ := os.ReadDir(parent)
				found := false
				for _, f := range files {
					data, _ := os.ReadFile(filepath.Join(parent, f.Name()))
					found = found || string(data) == "intruder"
				}
				if !found {
					t.Fatal("removed unknown temporary artifact")
				}
			}
			if err = verifyPrivateStage(o.ProfileDir, state.Transaction.StagePath, state.Transaction.StageIdentity, state.Transaction.StageSHA256, state.Transaction.StageMD5, state.Transaction.StageSize); mode != "source-replaced" && err != nil {
				t.Fatal("source modified")
			}
		})
	}
}

func TestExportDestinationExcludesRegistration(t *testing.T) {
	s, o, _, _, _ := recoveryFixture(t, OpUpload)
	parent := t.TempDir()
	d, err := acquireExportDestination(s.RegistryDir, o.ProfileDir, o.LocalDir, filepath.Join(parent, "saved"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	for _, registry := range []string{s.RegistryDir, filepath.Join(t.TempDir(), "other-registry")} {
		lease, err := registerDestination(registry, t.TempDir(), parent)
		if err == nil {
			_ = lease.Close()
			t.Fatal("profile claimed export parent during publication")
		}
	}
}

func TestExportRecoveryProfileExclusion(t *testing.T) {
	s, o, _, _, _ := recoveryFixture(t, OpUpload)
	l, err := privatefs.Acquire(filepath.Join(o.ProfileDir, "profile.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	if err = s.ExportRecovery(context.Background(), o, RecoveryExportOptions{Candidate: SideLocal, Output: filepath.Join(t.TempDir(), "saved")}); err == nil {
		t.Fatal("concurrent export accepted")
	}
}
