package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/torfstack/grove/internal/syncengine"
)

func recoveryArgs(command string) []string {
	return []string{"recovery", command, "--profile-dir", "p", "--remote-root", "r", "--local-dir", "l", "--token-file", "t"}
}
func TestRecoveryCLICommands(t *testing.T) {
	for _, command := range []string{"inspect", "retry", "export"} {
		for _, mode := range []string{"valid", "missing-binding", "extra", "unavailable", "bad-candidate", "missing-output"} {
			if (mode == "bad-candidate" || mode == "missing-output") && command != "export" {
				continue
			}
			t.Run(command+"/"+mode, func(t *testing.T) {
				called := 0
				check := func(o syncengine.Options) {
					called++
					if o.ProfileDir != "p" || o.RemoteRoot != "r" || o.LocalDir != "l" || o.TokenFile != "t" {
						t.Fatal("binding not forwarded")
					}
				}
				report := syncengine.RecoveryReport{Pending: true, Operation: syncengine.OpUpload, Phase: "upload-intent", Reason: "uncertain-write", Outcome: "pending", Local: syncengine.RecoveryStage{Status: "verified"}, Cloud: syncengine.RecoveryStage{Status: "none"}, Actions: []syncengine.RecoveryAction{"retry", "export-local"}}
				services := Services{InspectRecovery: func(_ context.Context, o syncengine.Options) (syncengine.RecoveryReport, error) {
					check(o)
					return report, nil
				}, RetryRecovery: func(_ context.Context, o syncengine.Options) (syncengine.RecoveryReport, error) {
					check(o)
					return syncengine.RecoveryReport{Outcome: "completed", Reason: "none"}, nil
				}, ExportRecovery: func(_ context.Context, o syncengine.Options, e syncengine.RecoveryExportOptions) error {
					check(o)
					if e.Candidate != syncengine.SideLocal || e.Output != "saved" {
						t.Fatal("export options lost")
					}
					return nil
				}}
				args := recoveryArgs(command)
				if command == "export" {
					args = append(args, "--candidate", "local", "--output", "saved")
				}
				switch mode {
				case "missing-binding":
					args = []string{"recovery", command}
				case "extra":
					args = append(args, "extra")
				case "unavailable":
					services = Services{}
				case "bad-candidate":
					args[len(args)-3] = "other"
				case "missing-output":
					args = args[:len(args)-2]
				}
				root := NewRoot(services)
				var out bytes.Buffer
				root.SetOut(&out)
				root.SetErr(&out)
				root.SetArgs(args)
				err := root.ExecuteContext(context.Background())
				if mode == "valid" {
					if err != nil || called != 1 {
						t.Fatalf("valid command failed %v", err)
					}
					if command == "inspect" && (!strings.Contains(out.String(), "uncertain-write") || !strings.Contains(out.String(), "export-local")) {
						t.Fatal("diagnosis omitted")
					}
					if command == "retry" && !strings.Contains(out.String(), "completed") {
						t.Fatal("completion omitted")
					}
					return
				}
				if err == nil || called != 0 {
					t.Fatal("invalid command reached service")
				}
			})
		}
	}
}

func TestRecoveryCLIRedactsCauses(t *testing.T) {
	secret := "https://private.invalid/token DRIVE-ID rev-secret stage/private source/private 0123456789abcdef"
	for _, command := range []string{"inspect", "retry", "export"} {
		for _, mode := range []string{"raw", "blocked", "cancel", "published"} {
			t.Run(command+"/"+mode, func(t *testing.T) {
				err := error(errors.New(secret))
				switch mode {
				case "blocked":
					err = &syncengine.RecoveryBlockedError{Reason: "uncertain-write"}
				case "cancel":
					err = fmt.Errorf("%s: %w", secret, context.Canceled)
				case "published":
					err = &syncengine.RecoveryExportError{Published: true, Cause: err}
				}
				report := syncengine.RecoveryReport{Pending: true, Operation: syncengine.OperationKind(secret), Phase: secret, Reason: syncengine.RecoveryReason(secret), Actions: []syncengine.RecoveryAction{syncengine.RecoveryAction(secret)}, Local: syncengine.RecoveryStage{Status: syncengine.RecoveryStageStatus(secret)}, Outcome: syncengine.RecoveryOutcome(secret)}
				services := Services{InspectRecovery: func(context.Context, syncengine.Options) (syncengine.RecoveryReport, error) { return report, err }, RetryRecovery: func(context.Context, syncengine.Options) (syncengine.RecoveryReport, error) { return report, err }, ExportRecovery: func(context.Context, syncengine.Options, syncengine.RecoveryExportOptions) error { return err }}
				root := NewRoot(services)
				var out bytes.Buffer
				root.SetOut(&out)
				root.SetErr(&out)
				args := recoveryArgs(command)
				if command == "export" {
					args = append(args, "--candidate", "local", "--output", "saved")
				}
				root.SetArgs(args)
				display := root.ExecuteContext(context.Background())
				if display == nil {
					t.Fatal("error reported success")
				}
				all := out.String() + display.Error()
				for _, s := range strings.Fields(secret) {
					if strings.Contains(all, s) {
						t.Fatalf("sensitive cause leaked %q", all)
					}
				}
				if strings.Contains(all, "Recovery completed") || strings.Contains(all, "Exported") {
					t.Fatal("failure announced success")
				}
				if mode == "published" && !strings.Contains(display.Error(), "output may already exist") {
					t.Fatal("publication warning lost")
				}
			})
		}
	}
}

func TestRecoveryCLIOutcomes(t *testing.T) {
	for _, outcome := range []syncengine.RecoveryOutcome{"none", "completed", "conflict-pending", "pending"} {
		t.Run(string(outcome), func(t *testing.T) {
			r := syncengine.RecoveryReport{Outcome: outcome, Reason: "none"}
			var err error
			if outcome == "pending" {
				r.Pending = true
				r.Reason = "uncertain-write"
				err = &syncengine.RecoveryBlockedError{Reason: r.Reason}
			}
			root := NewRoot(Services{RetryRecovery: func(context.Context, syncengine.Options) (syncengine.RecoveryReport, error) { return r, err }})
			var out bytes.Buffer
			root.SetOut(&out)
			root.SetArgs(recoveryArgs("retry"))
			got := root.Execute()
			if (got != nil) != (err != nil) || !strings.Contains(out.String(), string(outcome)) {
				t.Fatalf("outcome not reported %s %q %v", outcome, out.String(), got)
			}
		})
	}
}
