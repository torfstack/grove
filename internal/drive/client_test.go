package drive

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestListAllPages(t *testing.T)       { testPages(t, false, false) }
func TestEmptyPageContinues(t *testing.T) { testPages(t, false, true) }
func TestIncompleteListing(t *testing.T)  { testPages(t, true, false) }
func testPages(t *testing.T, incomplete, empty bool) {
	t.Helper()
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Query().Get("q") != "'parent' in parents and trashed = false" {
			t.Error("wrong query")
		}
		if r.URL.Query().Get("fields") == "" {
			t.Error("missing selected fields")
		}
		if calls == 1 {
			files := `[{"id":"one","name":"a","mimeType":"text/plain","size":"0","md5Checksum":"d41d8cd98f00b204e9800998ecf8427e","version":"1","ownedByMe":true,"capabilities":{"canDownload":true}}]`
			if empty {
				files = "[]"
			}
			_, _ = io.WriteString(w, `{"files":`+files+`,"nextPageToken":"next","incompleteSearch":`+map[bool]string{true: "true", false: "false"}[incomplete]+`}`)
		} else {
			if r.URL.Query().Get("pageToken") != "next" {
				t.Error("wrong token")
			}
			_, _ = io.WriteString(w, `{"files":[{"id":"two","name":"b","mimeType":"application/vnd.google-apps.folder","ownedByMe":true}]}`)
		}
	}))
	defer s.Close()
	api := NewClient(s.Client(), ClientOptions{BaseURL: s.URL, PageSize: 2})
	got, err := ListAll(context.Background(), api, "'parent' in parents and trashed = false")
	if incomplete {
		if err == nil || calls != 1 {
			t.Fatal("incomplete search accepted")
		}
		return
	}
	want := 2
	if empty {
		want = 1
	}
	if err != nil || len(got) != want || calls != 2 {
		t.Fatalf("pages: %v count %d", err, len(got))
	}
	if !empty && (!got[0].HasSize || got[0].Size != 0 || !got[0].CanDownload) {
		t.Fatal("metadata missing")
	}
}
func TestRepeatedPageToken(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, `{"nextPageToken":"same"}`) }))
	defer s.Close()
	_, err := ListAll(context.Background(), NewClient(s.Client(), ClientOptions{BaseURL: s.URL}), "query")
	if err == nil {
		t.Fatal("accepted repeated token")
	}
}
func TestMediaStreams(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("alt") != "media" {
			t.Error("missing media query")
		}
		_, _ = w.Write([]byte{0, 1, 255})
	}))
	defer s.Close()
	var b bytes.Buffer
	err := NewClient(s.Client(), ClientOptions{BaseURL: s.URL}).Download(context.Background(), "id", &b)
	if err != nil || !bytes.Equal(b.Bytes(), []byte{0, 1, 255}) {
		t.Fatal("wrong media", err)
	}
}
func TestCreateMultipart(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || !strings.Contains(r.Header.Get("Content-Type"), "multipart/related") {
			t.Error("wrong create request")
		}
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(data, []byte("payload")) || !bytes.Contains(data, []byte(`"grove_run":"run"`)) {
			t.Error("missing content/ownership")
		}
		_, _ = io.WriteString(w, `{"id":"created","name":"file"}`)
	}))
	defer s.Close()
	api := NewClient(s.Client(), ClientOptions{BaseURL: s.URL})
	f, err := api.Create(context.Background(), Create{Name: "file", MIMEType: "application/octet-stream", ParentID: "root", Properties: map[string]string{"grove_run": "run"}}, strings.NewReader("payload"))
	if err != nil || f.ID != "created" {
		t.Fatal("create failed", err)
	}
}
func TestTrash(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]bool
		_ = json.NewDecoder(r.Body).Decode(&body)
		if r.Method != "PATCH" || !body["trashed"] {
			t.Error("wrong trash")
		}
		_, _ = io.WriteString(w, `{"trashed":true}`)
	}))
	defer s.Close()
	if err := NewClient(s.Client(), ClientOptions{BaseURL: s.URL}).Trash(context.Background(), "id"); err != nil {
		t.Fatal(err)
	}
}
func TestNoCreateRetry(t *testing.T) {
	n := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		w.WriteHeader(500)
		_, _ = io.WriteString(w, "secret-sentinel")
	}))
	defer s.Close()
	_, err := NewClient(s.Client(), ClientOptions{BaseURL: s.URL}).Create(context.Background(), Create{Name: "x"}, nil)
	if err == nil || strings.Contains(err.Error(), "secret-sentinel") || n != 1 {
		t.Fatal("create retry or leaked error")
	}
}
func TestRetryCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	n := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { n++; cancel(); w.WriteHeader(429) }))
	defer s.Close()
	_, err := NewClient(s.Client(), ClientOptions{BaseURL: s.URL}).Get(ctx, "id")
	if err == nil || n != 1 {
		t.Fatal("ignored cancellation")
	}
}
func TestCrossOriginRedirect(t *testing.T) {
	targetCalls := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetCalls++ }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer source.Close()
	var b bytes.Buffer
	err := NewClient(source.Client(), ClientOptions{BaseURL: source.URL}).Download(context.Background(), "id", &b)
	if err == nil || targetCalls != 0 {
		t.Fatal("redirect followed")
	}
}
func TestMetadataFields(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"id":"f","size":"7","version":"9","ownedByMe":true,"capabilities":{"canDownload":true}}`)
	}))
	defer s.Close()
	f, err := NewClient(s.Client(), ClientOptions{BaseURL: s.URL}).Get(context.Background(), "f")
	if err != nil || f.Size != 7 || f.Version != "9" || !f.OwnedByMe {
		t.Fatal("metadata lost", err)
	}
}
func TestErrorsRedacted(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		_, _ = io.WriteString(w, "secret-sentinel")
	}))
	defer s.Close()
	_, err := NewClient(s.Client(), ClientOptions{BaseURL: s.URL}).Get(context.Background(), "private-id")
	if err == nil || strings.Contains(err.Error(), "secret-sentinel") || strings.Contains(err.Error(), "private-id") {
		t.Fatal("unsanitized error")
	}
}
