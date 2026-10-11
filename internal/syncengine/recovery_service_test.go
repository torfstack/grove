package syncengine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/torfstack/grove/internal/drive"
	"github.com/torfstack/grove/internal/privatefs"
)

func recoveryFixture(t *testing.T, kind OperationKind) (Service, Options, *State, *executor, *uploadAPI) {
	t.Helper()
	opts, registry := options(t)
	for _, p := range []string{opts.ProfileDir, opts.LocalDir} {
		if err := os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	canonical, err := canonicalOptions(opts)
	if err != nil {
		t.Fatal(err)
	}
	opts = canonical
	if err = os.WriteFile(filepath.Join(opts.LocalDir, "a"), []byte("edit"), 0600); err != nil {
		t.Fatal(err)
	}
	remote := file("a", "a")
	remote.Size, remote.MD5 = 3, md5Hex([]byte("old"))
	baseline := baselineFile("a", "a", []byte("old"))
	baseline.ModifiedTime, baseline.HeadRevisionID = remote.ModifiedTime, remote.HeadRevisionID
	state := &State{Version: 3, Binding: Binding{RemoteRoot: opts.RemoteRoot, LocalDir: opts.LocalDir, TokenFile: opts.TokenFile}, PopulationComplete: true, ConflictPolicy: ConflictAsk}
	op := Operation{Kind: kind, Entry: Entry{Path: "a", Remote: remote}, Local: LocalEntry{Path: "a", Kind: "file", SHA256: hashText("edit"), Size: 4}}
	if kind == OpUpload {
		state.Completed = []Completed{baseline}
		op.Before = []Completed{baseline}
	} else {
		op.OperationMarker = "marker"
	}
	api := &uploadAPI{file: remote, data: []byte("old"), pinned: map[string]bool{}, revisions: []drive.Revision{{ID: remote.HeadRevisionID, ModifiedTime: remote.ModifiedTime, MD5: remote.MD5, Size: remote.Size, HasSize: true}}}
	root, err := os.OpenRoot(opts.LocalDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	ex := &executor{root: root, profileDir: opts.ProfileDir, remoteRoot: opts.RemoteRoot, api: api, mutation: api, state: state, save: func() error { return saveState(opts.ProfileDir, *state) }, resumeUploads: true}
	if err = ex.prepareUpload(context.Background(), op); err != nil {
		t.Fatal(err)
	}
	service := Service{RegistryDir: registry, OpenAPI: func(context.Context, string, string) (drive.API, func() error, error) {
		t.Fatal("offline operation opened provider")
		return nil, nil, nil
	}}
	return service, opts, state, ex, api
}

func TestInspectRecoveryOffline(t *testing.T) {
	for _, kind := range []OperationKind{OpUpload, OpCreate} {
		t.Run(string(kind), func(t *testing.T) {
			s, o, state, _, _ := recoveryFixture(t, kind)
			before, _ := os.ReadFile(filepath.Join(o.ProfileDir, "state.json"))
			r, err := s.InspectRecovery(context.Background(), o)
			if err != nil || !r.Pending || r.Operation != kind || r.Phase != "staged" || r.Reason != "remote-check-required" || r.Local.Status != "verified" {
				t.Fatalf("wrong diagnosis: %+v %v", r, err)
			}
			after, _ := os.ReadFile(filepath.Join(o.ProfileDir, "state.json"))
			if string(before) != string(after) {
				t.Fatal("inspection changed state")
			}
			state.Transaction = nil
			if err = saveState(o.ProfileDir, *state); err != nil {
				t.Fatal(err)
			}
			r, err = s.InspectRecovery(context.Background(), o)
			if err != nil || r.Pending || r.Reason != "none" {
				t.Fatalf("idle: %+v %v", r, err)
			}
		})
	}
}

func TestInspectRecoveryStageStates(t *testing.T) {
	for _, mode := range []string{"missing", "changed", "symlink", "identity", "partial", "cloud", "committed"} {
		t.Run(mode, func(t *testing.T) {
			s, o, state, _, _ := recoveryFixture(t, OpUpload)
			j := state.Transaction
			want := "unavailable"
			switch mode {
			case "missing", "committed":
				if err := os.Remove(filepath.Join(o.ProfileDir, j.StagePath)); err != nil {
					t.Fatal(err)
				}
			case "changed":
				if err := os.WriteFile(filepath.Join(o.ProfileDir, j.StagePath), []byte("evil"), 0600); err != nil {
					t.Fatal(err)
				}
			case "identity":
				j.StageIdentity.Inode++
			case "symlink":
				if err := os.Remove(filepath.Join(o.ProfileDir, j.StagePath)); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(o.LocalDir, "a"), filepath.Join(o.ProfileDir, j.StagePath)); err != nil {
					t.Fatal(err)
				}
			case "partial", "cloud":
				path, id := createConflictTestStage(t, o.ProfileDir, ".grove-cloud-conflict-test", []byte("cloud"))
				j.CloudStagePath, j.CloudStageIdentity = path, &id
				j.CloudMD5, j.CloudSize, j.CloudRevisionID = md5Hex([]byte("cloud")), 5, "competitor"
				j.Phase = "capture-intent"
				if mode == "cloud" {
					j.Phase = "conflict-captured"
					j.CloudSHA256 = hashText("cloud")
				}
				want = "verified"
			}
			if mode == "partial" || mode == "cloud" || mode == "committed" {
				j.PinnedRevisionID = j.PreflightRevisionID
				j.After = []Completed{baselineFile("a", "a", []byte("edit"))}
				j.After[0].HeadRevisionID = "rev-2"
				j.After[0].ModifiedTime = "2026-01-02T00:00:00Z"
			}
			if mode == "committed" {
				j.Phase = "committed"
				state.Completed = j.After
			}
			if err := saveState(o.ProfileDir, *state); err != nil {
				t.Fatal(err)
			}
			r, err := s.InspectRecovery(context.Background(), o)
			if err != nil || string(r.Local.Status) != want {
				t.Fatalf("stage: %+v %v", r, err)
			}
			if mode == "partial" && r.Cloud.Status != "partial" {
				t.Fatal("partial cloud exportable")
			}
			if mode == "cloud" && r.Cloud.Status != "verified" {
				t.Fatal("verified cloud unavailable")
			}
		})
	}
}

func TestInspectRecoveryRejectsInvalidProfile(t *testing.T) {
	for _, mode := range []string{"absent", "binding", "malformed", "unpopulated"} {
		t.Run(mode, func(t *testing.T) {
			s, o, state, _, _ := recoveryFixture(t, OpUpload)
			switch mode {
			case "absent":
				if err := os.Remove(filepath.Join(o.ProfileDir, "state.json")); err != nil {
					t.Fatal(err)
				}
			case "binding":
				o.RemoteRoot = "other"
			case "malformed":
				state.Transaction.Phase = "bogus"
			case "unpopulated":
				state.PopulationComplete = false
			}
			if mode == "malformed" || mode == "unpopulated" {
				if err := saveState(o.ProfileDir, *state); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.InspectRecovery(context.Background(), o); err == nil {
				t.Fatal("invalid profile accepted")
			}
		})
	}
}

func TestInspectRecoveryPendingDownloadUnsupported(t *testing.T) {
	s, o, state, _, _ := recoveryFixture(t, OpUpload)
	state.Transaction = nil
	state.Pending = &Pending{Entry: Entry{Path: "b", Remote: file("b", "b")}, Phase: "intent"}
	if err := saveState(o.ProfileDir, *state); err != nil {
		t.Fatal(err)
	}
	r, err := s.InspectRecovery(context.Background(), o)
	if err != nil || !r.Pending || r.Reason != "unsupported-operation" || !reflect.DeepEqual(r.Actions, []RecoveryAction{"sync"}) {
		t.Fatalf("pending download: %+v %v", r, err)
	}
}

func TestRecoveryBlockedErrorSanitized(t *testing.T) {
	err := &RecoveryBlockedError{Reason: RecoveryReason("https://sensitive.invalid/token")}
	if err.Error() != "upload recovery remains blocked" {
		t.Fatal("untrusted reason leaked")
	}
}

func TestInspectRecoveryExclusionAndCancellation(t *testing.T) {
	s, o, _, _, _ := recoveryFixture(t, OpUpload)
	l, err := privatefs.Acquire(filepath.Join(o.ProfileDir, "profile.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.InspectRecovery(context.Background(), o); err == nil {
		t.Fatal("concurrent inspection accepted")
	}
	if err = l.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = s.InspectRecovery(ctx, o); !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation: %v", err)
	}
}

func TestRetryRecoveryOnlyOriginalTransaction(t *testing.T) {
	for _, policy := range []ConflictPolicy{ConflictAsk, ConflictKeepLocal, ConflictKeepCloud} {
		for _, competitor := range []bool{false, true} {
			t.Run(string(policy)+fmt.Sprint(competitor), func(t *testing.T) {
				s, o, state, ex, base := recoveryFixture(t, OpUpload)
				setRecoveredHead(t, ex, base, OpUpload)
				state.ConflictPolicy = policy
				if err := saveState(o.ProfileDir, *state); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(o.LocalDir, "unrelated"), []byte("new"), 0600); err != nil {
					t.Fatal(err)
				}
				if competitor {
					base.revisionData = map[string][]byte{"racer": []byte("cloud")}
					base.revisions = append(base.revisions, drive.Revision{ID: "racer", ModifiedTime: "2026-01-01T12:00:00Z", MD5: md5Hex([]byte("cloud")), Size: 5, HasSize: true})
				}
				api := &recoveryAPI{uploadAPI: base}
				opened, closed := 0, 0
				s.OpenAPI = func(_ context.Context, _ string, access string) (drive.API, func() error, error) {
					opened++
					if access != "read-write" {
						t.Fatal("wrong access")
					}
					return api, func() error { closed++; return nil }, nil
				}
				r, err := s.RetryRecovery(context.Background(), o)
				if err != nil || r.Pending || opened != 1 || closed != 1 {
					t.Fatalf("retry: %+v %v", r, err)
				}
				want := RecoveryOutcome("completed")
				if competitor {
					want = "conflict-pending"
				}
				if r.Outcome != want {
					t.Fatalf("outcome %+v", r)
				}
				loaded, err := loadState(o.ProfileDir, state.Binding)
				if err != nil || loaded.Transaction != nil || len(loaded.Completed) != 1 {
					t.Fatalf("state: %v", err)
				}
				if competitor && (len(loaded.Conflicts) != 1 || loaded.Conflicts[0].SelectedSide != "") {
					t.Fatal("policy applied during retry")
				}
				data, err := os.ReadFile(filepath.Join(o.LocalDir, "unrelated"))
				if err != nil || string(data) != "new" {
					t.Fatal("unrelated local data modified")
				}
			})
		}
	}
}

func TestRetryRecoveryResourceBoundaries(t *testing.T) {
	for _, mode := range []string{"idle", "unsupported", "blocked", "provider", "close", "cancel", "exclusion"} {
		t.Run(mode, func(t *testing.T) {
			s, o, state, ex, base := recoveryFixture(t, OpUpload)
			setRecoveredHead(t, ex, base, OpUpload)
			api := &recoveryAPI{uploadAPI: base}
			sentinel := errors.New("https://private.invalid/session")
			calls, closed := 0, 0
			if mode == "idle" {
				state.Transaction = nil
			}
			if mode == "unsupported" {
				state.Transaction = nil
				state.Pending = &Pending{Entry: Entry{Path: "b", Remote: file("b", "b")}, Phase: "intent"}
			}
			if err := saveState(o.ProfileDir, *state); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s.OpenAPI = func(context.Context, string, string) (drive.API, func() error, error) {
				calls++
				if mode == "provider" {
					return nil, nil, sentinel
				}
				if mode == "cancel" {
					cancel()
				}
				l, err := privatefs.Acquire(filepath.Join(o.ProfileDir, "profile.lock"))
				if err == nil {
					_ = l.Close()
					t.Fatal("profile lock released during recovery")
				}
				if mode == "exclusion" {
					if _, err = s.Run(context.Background(), o); err == nil {
						t.Fatal("sync entered locked profile")
					}
				}
				return api, func() error {
					closed++
					if mode == "close" {
						return sentinel
					}
					return nil
				}, nil
			}
			if mode == "blocked" {
				base.file.HeadRevisionID = "rev-1"
			}
			r, err := s.RetryRecovery(ctx, o)
			switch mode {
			case "idle":
				if err != nil || calls != 0 || r.Pending {
					t.Fatal("idle opened provider")
				}
			case "unsupported":
				if err == nil || calls != 0 || r.Reason != "unsupported-operation" {
					t.Fatalf("unsupported %+v %v", r, err)
				}
			case "blocked":
				var b *RecoveryBlockedError
				if !errors.As(err, &b) || !r.Pending || r.Reason != "uncertain-write" {
					t.Fatalf("blocked %+v %v", r, err)
				}
			case "provider", "close":
				if !errors.Is(err, sentinel) {
					t.Fatalf("failure swallowed: %v", err)
				}
			case "cancel":
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancellation swallowed %v", err)
				}
			default:
				if err != nil {
					t.Fatal(err)
				}
			}
			if calls == 1 && mode != "provider" && closed != 1 {
				t.Fatal("API not closed")
			}
		})
	}
}
