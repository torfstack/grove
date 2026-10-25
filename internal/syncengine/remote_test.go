package syncengine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/torfstack/grove/internal/drive"
	"github.com/torfstack/grove/internal/privatefs"
)

type remoteFeedAPI struct {
	drive.API
	start func(context.Context) (string, error)
	list  func(context.Context, string) (drive.ChangesPage, error)
}

func (a *remoteFeedAPI) StartChanges(ctx context.Context) (string, error) {
	if a.start != nil {
		return a.start(ctx)
	}
	return "fresh", nil
}
func (a *remoteFeedAPI) ListChanges(ctx context.Context, token string) (drive.ChangesPage, error) {
	return a.list(ctx, token)
}

func TestRemoteCheckProfileBoundary(t *testing.T) {
	for _, kind := range []string{"initialize", "missing", "rebound", "busy-profile", "busy-token"} {
		t.Run(kind, func(t *testing.T) {
			opts, binding, _ := savedProfile(t, true)
			before, err := os.ReadFile(filepath.Join(opts.ProfileDir, "state.json"))
			if err != nil {
				t.Fatal(err)
			}
			opened, closed := false, false
			service := Service{OpenAPI: func(_ context.Context, token, access string) (drive.API, func() error, error) {
				opened = true
				if token != binding.TokenFile || access != "read-only" {
					t.Fatal("wrong auth binding/scope")
				}
				if kind == "busy-token" {
					return nil, nil, privatefs.ErrLocked
				}
				return &remoteFeedAPI{}, func() error { closed = true; return nil }, nil
			}}
			profile := opts.ProfileDir
			expected := binding
			switch kind {
			case "missing":
				profile = filepath.Join(t.TempDir(), "absent")
			case "rebound":
				expected.RemoteRoot = "different"
			case "busy-profile":
				lock, err := privatefs.AcquireLease(filepath.Join(profile, "profile.lock"))
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = lock.Close() }()
			}
			result, err := service.CheckRemoteChanges(context.Background(), profile, expected, "")
			if kind == "initialize" {
				if err != nil || !opened || !closed || result.Cursor != "fresh" || !result.Changed {
					t.Fatalf("initial check: %+v %v", result, err)
				}
			} else {
				if err == nil {
					t.Fatal("invalid/busy profile accepted")
				}
				if kind != "busy-token" && opened {
					t.Fatal("invalid profile authenticated")
				}
				if strings.HasPrefix(kind, "busy") && !errors.Is(err, privatefs.ErrLocked) {
					t.Fatal("contention identity lost")
				}
			}
			after, readErr := os.ReadFile(filepath.Join(opts.ProfileDir, "state.json"))
			if readErr != nil || string(after) != string(before) {
				t.Fatal("remote check mutated profile state")
			}
			if kind == "missing" {
				if _, err := os.Stat(profile); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("missing profile recreated")
				}
			}
		})
	}
}
func TestRemoteCheckPagination(t *testing.T) {
	for _, tc := range []struct {
		name           string
		pages          []drive.ChangesPage
		fail           int
		changed, valid bool
		want           string
	}{
		{"empty", []drive.ChangesPage{{StartToken: "end"}}, 0, false, true, "end"},
		{"pages", []drive.ChangesPage{{NextToken: "next", Changed: true}, {StartToken: "end"}}, 0, true, true, "end"},
		{"both", []drive.ChangesPage{{NextToken: "next", StartToken: "end", Changed: true}}, 0, true, false, "start"},
		{"missing-end", []drive.ChangesPage{{}}, 0, false, false, "start"},
		{"cycle", []drive.ChangesPage{{NextToken: "start", Changed: true}}, 0, true, false, "start"},
		{"later-error", []drive.ChangesPage{{NextToken: "next", Changed: true}, {}}, 2, true, false, "start"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts, binding, _ := savedProfile(t, true)
			calls := 0
			api := &remoteFeedAPI{list: func(_ context.Context, token string) (drive.ChangesPage, error) {
				calls++
				if calls > len(tc.pages) {
					t.Fatal("unbounded paging")
				}
				if tc.fail == calls {
					return drive.ChangesPage{}, errors.New("private-transport-secret")
				}
				return tc.pages[calls-1], nil
			}}
			service := Service{OpenAPI: func(context.Context, string, string) (drive.API, func() error, error) {
				return api, func() error { return nil }, nil
			}}
			result, err := service.CheckRemoteChanges(context.Background(), opts.ProfileDir, binding, "start")
			if (err == nil) != tc.valid || result.Cursor != tc.want || result.Changed != tc.changed {
				t.Fatalf("result=%+v valid=%t err=%v", result, tc.valid, err)
			}
			if err != nil && strings.Contains(err.Error(), "private") {
				t.Fatal("check leaked provider details")
			}
		})
	}
	t.Run("cap", func(t *testing.T) {
		opts, binding, _ := savedProfile(t, true)
		calls := 0
		api := &remoteFeedAPI{list: func(context.Context, string) (drive.ChangesPage, error) {
			calls++
			return drive.ChangesPage{NextToken: fmt.Sprintf("page-%d", calls), Changed: true}, nil
		}}
		s := Service{OpenAPI: func(context.Context, string, string) (drive.API, func() error, error) {
			return api, func() error { return nil }, nil
		}}
		result, err := s.CheckRemoteChanges(context.Background(), opts.ProfileDir, binding, "start")
		if err == nil || calls != 100 || result.Cursor != "start" || !result.Changed {
			t.Fatalf("pagination cap: calls=%d result=%+v err=%v", calls, result, err)
		}
	})
}
func TestRemoteCheckHTTP(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("pageToken") {
		case "":
			_ = json.NewEncoder(w).Encode(map[string]any{"startPageToken": "start"})
		case "start":
			_ = json.NewEncoder(w).Encode(map[string]any{"nextPageToken": "next", "changes": []any{map[string]string{"changeType": "file"}}})
		case "next":
			_ = json.NewEncoder(w).Encode(map[string]any{"newStartPageToken": "end"})
		default:
			t.Error("unexpected cursor")
		}
	}))
	defer server.Close()
	opts, binding, _ := savedProfile(t, true)
	s := Service{OpenAPI: func(context.Context, string, string) (drive.API, func() error, error) {
		return drive.NewClient(server.Client(), drive.ClientOptions{BaseURL: server.URL}), func() error { return nil }, nil
	}}
	first, err := s.CheckRemoteChanges(context.Background(), opts.ProfileDir, binding, "")
	if err != nil || first.Cursor != "start" || !first.Changed {
		t.Fatal(first, err)
	}
	result, err := s.CheckRemoteChanges(context.Background(), opts.ProfileDir, binding, first.Cursor)
	if err != nil || result.Cursor != "end" || !result.Changed {
		t.Fatal(result, err)
	}
}
func TestRemoteCheckCleanup(t *testing.T) {
	opts, binding, _ := savedProfile(t, true)
	fault := errors.New("private-session-cleanup")
	cause := errors.New("private-signal")
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(context.Canceled)
	api := &remoteFeedAPI{list: func(context.Context, string) (drive.ChangesPage, error) {
		cancel(cause)
		return drive.ChangesPage{Changed: true}, context.Canceled
	}}
	s := Service{OpenAPI: func(context.Context, string, string) (drive.API, func() error, error) {
		return api, func() error { return fault }, nil
	}}
	result, err := s.CheckRemoteChanges(ctx, opts.ProfileDir, binding, "start")
	if !errors.Is(err, ErrRemoteCheckCleanup) || !errors.Is(err, fault) || !errors.Is(err, cause) || result.Cursor != "start" || !result.Changed || strings.Contains(err.Error(), "private") {
		t.Fatalf("cleanup/cancel lost or exposed: %+v %v", result, err)
	}
	joined := error(context.Canceled)
	closeRemoteCheck(func() error { return fault }, &joined)
	if !errors.Is(joined, context.Canceled) || !errors.Is(joined, fault) || !errors.Is(joined, ErrRemoteCheckCleanup) {
		t.Fatal("profile/session close classifier dropped cause")
	}
	lock, lockErr := privatefs.AcquireLease(filepath.Join(opts.ProfileDir, "profile.lock"))
	if lockErr != nil {
		t.Fatal("profile lease retained")
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
}
