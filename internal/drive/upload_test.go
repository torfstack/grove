package drive

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestGenerateIDs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, "/files/generateIds") || r.URL.Query().Get("count") != "2" || r.URL.Query().Get("space") != "drive" {
			t.Errorf("unexpected ID request: %s %s", r.Method, r.URL.RequestURI())
		}
		_, _ = io.WriteString(w, "{\"ids\":[\"first\",\"second\"]}")
	}))
	defer server.Close()
	ids, err := NewClient(server.Client(), ClientOptions{BaseURL: server.URL + "/drive/v3"}).GenerateIDs(context.Background(), 2)
	if err != nil || !reflect.DeepEqual(ids, []string{"first", "second"}) {
		t.Fatalf("generated IDs %v: %v", ids, err)
	}
}

func TestCreateWithIDStreamsResumableChunks(t *testing.T) {
	content := bytes.Repeat([]byte("x"), int(uploadChunkSize+3))
	var ranges []string
	var received int
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/upload/drive/v3/files":
			var metadata map[string]any
			if r.Method != http.MethodPost || r.URL.Query().Get("uploadType") != "resumable" || r.Header.Get("X-Upload-Content-Length") != fmt.Sprint(len(content)) {
				t.Errorf("wrong resumable create request: %s %s", r.Method, r.URL.RequestURI())
			}
			if err := json.NewDecoder(r.Body).Decode(&metadata); err != nil || metadata["id"] != "allocated" || metadata["name"] != "file.bin" {
				t.Errorf("wrong create metadata: %v %v", metadata, err)
			}
			w.Header().Set("Location", server.URL+"/session")
			w.WriteHeader(http.StatusOK)
		case "/session":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("cannot read chunk: %v", err)
			}
			received += len(body)
			ranges = append(ranges, r.Header.Get("Content-Range"))
			if len(ranges) == 1 {
				w.Header().Set("Range", fmt.Sprintf("bytes=0-%d", len(body)-1))
				w.WriteHeader(308)
				return
			}
			_, _ = io.WriteString(w, "{\"id\":\"allocated\",\"version\":\"2\",\"md5Checksum\":\"d41d8cd98f00b204e9800998ecf8427e\"}")
		default:
			t.Errorf("unexpected request path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	api := NewClient(server.Client(), ClientOptions{BaseURL: server.URL + "/drive/v3"})
	file, err := api.CreateWithID(context.Background(), "allocated", Create{Name: "file.bin", MIMEType: "application/octet-stream"}, bytes.NewReader(content), int64(len(content)))
	if err != nil || file.ID != "allocated" {
		t.Fatalf("create result %+v: %v", file, err)
	}
	wantRanges := []string{fmt.Sprintf("bytes 0-%d/%d", uploadChunkSize-1, len(content)), fmt.Sprintf("bytes %d-%d/%d", uploadChunkSize, len(content)-1, len(content))}
	if !reflect.DeepEqual(ranges, wantRanges) || received != len(content) {
		t.Fatalf("ranges %v and received %d bytes", ranges, received)
	}
}

func TestUpdateContentUploadsZeroBytes(t *testing.T) {
	requests := 0
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path == "/upload/drive/v3/files/file" {
			if r.Method != http.MethodPatch || r.URL.Query().Get("uploadType") != "resumable" || r.Header.Get("X-Upload-Content-Length") != "0" {
				t.Errorf("wrong update initiation: %s %s", r.Method, r.URL.RequestURI())
			}
			w.Header().Set("Location", server.URL+"/session")
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path != "/session" || r.Method != http.MethodPut || r.Header.Get("Content-Range") != "bytes */0" {
			t.Errorf("wrong zero-byte upload: %s %s range=%q", r.Method, r.URL.RequestURI(), r.Header.Get("Content-Range"))
		}
		body, err := io.ReadAll(r.Body)
		if err != nil || len(body) != 0 {
			t.Errorf("zero-byte request had body: %v", err)
		}
		_, _ = io.WriteString(w, "{\"id\":\"file\",\"version\":\"3\"}")
	}))
	defer server.Close()
	api := NewClient(server.Client(), ClientOptions{BaseURL: server.URL + "/drive/v3"})
	file, err := api.UpdateContent(context.Background(), "file", Update{ContentChanged: true}, strings.NewReader(""), 0)
	if err != nil || file.Version != "3" || requests != 2 {
		t.Fatalf("zero-byte update %+v, requests %d: %v", file, requests, err)
	}
}

func TestRevisionMethodsListPinAndDownload(t *testing.T) {
	var downloaded bytes.Buffer
	var pinStates []bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/files/file/revisions"):
			if r.URL.Query().Get("pageToken") == "next" {
				_, _ = io.WriteString(w, "{\"revisions\":[{\"id\":\"r2\",\"keepForever\":true,\"md5Checksum\":\"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\",\"size\":\"3\"}]}")
				return
			}
			_, _ = io.WriteString(w, "{\"revisions\":[{\"id\":\"r1\",\"keepForever\":false,\"md5Checksum\":\"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\",\"size\":\"4\"}],\"nextPageToken\":\"next\"}")
		case r.Method == http.MethodPatch && strings.HasSuffix(r.URL.Path, "/revisions/r1"):
			var body map[string]bool
			if json.NewDecoder(r.Body).Decode(&body) != nil {
				t.Error("cannot decode revision request")
			}
			pinStates = append(pinStates, body["keepForever"])
			_, _ = fmt.Fprintf(w, "{\"id\":\"r1\",\"keepForever\":%t}", body["keepForever"])
		case r.Method == http.MethodGet && r.URL.Query().Get("alt") == "media" && strings.HasSuffix(r.URL.Path, "/revisions/r1"):
			_, _ = io.WriteString(w, "revision-bytes")
		default:
			t.Errorf("unexpected revision request: %s %s", r.Method, r.URL.RequestURI())
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	api := NewClient(server.Client(), ClientOptions{BaseURL: server.URL + "/drive/v3"})
	revisions, err := api.ListRevisions(context.Background(), "file")
	if err != nil || len(revisions) != 2 || revisions[0].ID != "r1" || revisions[1].Size != 3 || !revisions[1].KeepForever {
		t.Fatalf("revisions %+v: %v", revisions, err)
	}
	if err = api.SetRevisionKeepForever(context.Background(), "file", "r1", true); err != nil {
		t.Fatal("pin failed", err)
	}
	if err = api.SetRevisionKeepForever(context.Background(), "file", "r1", false); err != nil {
		t.Fatal("unpin failed", err)
	}
	if !reflect.DeepEqual(pinStates, []bool{true, false}) {
		t.Fatalf("pin states %v", pinStates)
	}
	if err = api.DownloadRevision(context.Background(), "file", "r1", &downloaded); err != nil || downloaded.String() != "revision-bytes" {
		t.Fatalf("revision download %q: %v", downloaded.String(), err)
	}
}

func TestUploadInitiationDoesNotRetry(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, "private-session-sentinel")
	}))
	defer server.Close()
	api := NewClient(server.Client(), ClientOptions{BaseURL: server.URL + "/drive/v3"})
	_, err := api.CreateWithID(context.Background(), "allocated", Create{Name: "file", MIMEType: "application/octet-stream"}, strings.NewReader("data"), 4)
	if err == nil || calls != 1 || strings.Contains(err.Error(), "private-session-sentinel") {
		t.Fatalf("mutation retried or leaked response: calls=%d err=%v", calls, err)
	}
}

func TestUploadCancellationDoesNotRetry(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewClient(server.Client(), ClientOptions{BaseURL: server.URL + "/drive/v3"}).CreateWithID(ctx, "allocated", Create{Name: "file", MIMEType: "application/octet-stream"}, strings.NewReader("data"), 4)
	if err == nil || calls != 0 {
		t.Fatalf("cancelled upload reached Drive: calls=%d err=%v", calls, err)
	}
}

func TestListRevisionsRetriesTemporaryRead(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, "{\"revisions\":[]}")
	}))
	defer server.Close()
	revisions, err := NewClient(server.Client(), ClientOptions{BaseURL: server.URL + "/drive/v3"}).ListRevisions(context.Background(), "file")
	if err != nil || len(revisions) != 0 || calls != 2 {
		t.Fatalf("temporary revision listing failure was not retried safely: calls=%d err=%v", calls, err)
	}
}
