package drive

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStartChanges(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/changes/startPageToken" || r.URL.Query().Get("fields") != "startPageToken" || r.URL.Query().Has("pageToken") || r.URL.Query().Get("supportsAllDrives") != "false" {
			t.Error("incorrect cursor request")
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"startPageToken": "opaque +/&?cursor"})
	}))
	defer server.Close()
	c := NewClient(server.Client(), ClientOptions{BaseURL: server.URL})
	token, err := c.StartChanges(context.Background())
	if err != nil || token != "opaque +/&?cursor" {
		t.Fatalf("cursor: %q %v", token, err)
	}
}
func TestListChanges(t *testing.T) {
	for _, tc := range []struct {
		name, body  string
		changed     bool
		next, start string
	}{
		{"empty", `{"newStartPageToken":"end","changes":[]}`, false, "", "end"},
		{"removed", `{"nextPageToken":"next","changes":[{"changeType":"file"}]}`, true, "next", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			token := "opaque +/&?cursor"
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				q := r.URL.Query()
				if r.URL.Path != "/changes" || q.Get("pageToken") != token || q.Get("spaces") != "drive" || q.Get("restrictToMyDrive") != "true" || q.Get("includeRemoved") != "true" || q.Get("includeItemsFromAllDrives") != "false" || q.Get("supportsAllDrives") != "false" || strings.Contains(q.Get("fields"), "fileId") {
					t.Error("incorrect feed scope/query")
				}
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			c := NewClient(server.Client(), ClientOptions{BaseURL: server.URL})
			page, err := c.ListChanges(context.Background(), token)
			if err != nil || page.Changed != tc.changed || page.NextToken != tc.next || page.StartToken != tc.start {
				t.Fatalf("page=%+v err=%v", page, err)
			}
		})
	}
}
func TestChangesRejectMalformed(t *testing.T) {
	for _, body := range []string{`{`, `{"startPageToken":""}`, `{"startPageToken":42}`, `{"startPageToken":"` + strings.Repeat("a", 64*1024+1) + `"}`, strings.Repeat(" ", 1024*1024+1) + `{"startPageToken":"safe"}`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) }))
		c := NewClient(server.Client(), ClientOptions{BaseURL: server.URL})
		if _, err := c.StartChanges(context.Background()); err == nil {
			t.Error("malformed cursor response accepted")
		}
		server.Close()
	}
	for _, body := range []string{strings.Repeat(" ", 1024*1024+1) + `{"newStartPageToken":"safe"}`, `{"changes":{}}`, `{"nextPageToken":7}`, `{"newStartPageToken":"` + strings.Repeat("a", 64*1024+1) + `"}`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) }))
		c := NewClient(server.Client(), ClientOptions{BaseURL: server.URL})
		if _, err := c.ListChanges(context.Background(), "start"); err == nil {
			t.Error("malformed page accepted")
		}
		server.Close()
	}
	c := NewClient(&http.Client{Transport: changesTransport(func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid input made HTTP request")
		return nil, nil
	})}, ClientOptions{})
	for _, token := range []string{"", strings.Repeat("a", 64*1024+1)} {
		if _, err := c.ListChanges(context.Background(), token); err == nil {
			t.Error("invalid cursor input accepted")
		}
	}
}

type changesTransport func(*http.Request) (*http.Response, error)

func (f changesTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type changesCloseBody struct {
	io.Reader
	err error
}

func (b changesCloseBody) Close() error { return b.err }
func TestChangesCancellationRedaction(t *testing.T) {
	fault := errors.New("private-close-secret")
	for _, cancelled := range []bool{false, true} {
		client := &http.Client{Transport: changesTransport(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: changesCloseBody{Reader: strings.NewReader(`{"startPageToken":"safe"}`), err: fault}}, nil
		})}
		c := NewClient(client, ClientOptions{BaseURL: "https://private.example"})
		_, err := c.StartChanges(context.Background())
		if !errors.Is(err, fault) || strings.Contains(err.Error(), "private") {
			t.Fatalf("close failure lost/exposed: %v", err)
		}
		if cancelled {
			ctx, cancel := context.WithCancelCause(context.Background())
			cause := errors.New("private-cancel-cause")
			cancel(cause)
			c = NewClient(&http.Client{Transport: changesTransport(func(r *http.Request) (*http.Response, error) { return nil, context.Cause(r.Context()) })}, ClientOptions{BaseURL: "https://private.example"})
			_, err = c.ListChanges(ctx, "private-cursor")
			if !errors.Is(err, cause) || strings.Contains(err.Error(), "private") {
				t.Fatalf("cancel cause lost/exposed: %v", err)
			}
		}
	}
}
