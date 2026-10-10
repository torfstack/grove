package main

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"github.com/torfstack/grove/internal/auth"
	"github.com/torfstack/grove/internal/cli"
	"github.com/torfstack/grove/internal/drive"
	"github.com/torfstack/grove/internal/fixture"
	"golang.org/x/oauth2"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCLIWorkflowHTTP(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
	clientPath := filepath.Join(dir, "client.json")
	if err := os.WriteFile(clientPath, []byte(`{"installed":{"client_id":"test","client_secret":"synthetic"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	token := filepath.Join(dir, "token.json")
	scope, _ := auth.Scope("read-write")
	if err := auth.SaveRecord(token, auth.Record{Version: 1, ClientID: "test", ClientSecretPath: clientPath, Scopes: []string{scope}, Token: &oauth2.Token{AccessToken: "synthetic", RefreshToken: "synthetic", TokenType: "Bearer", Expiry: time.Now().Add(time.Hour)}}); err != nil {
		t.Fatal(err)
	}
	files := map[string]map[string]any{}
	contents := map[string][]byte{}
	mediaCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic" {
			t.Error("missing auth")
		}
		w.Header().Set("Content-Type", "application/json")
		id := strings.TrimPrefix(r.URL.Path, "/files/")
		switch r.Method {
		case "POST":
			var metadata map[string]any
			var data []byte
			if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
				_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
				if err != nil {
					t.Error(err)
					return
				}
				reader := multipart.NewReader(r.Body, params["boundary"])
				part, err := reader.NextPart()
				if err != nil {
					t.Error(err)
					return
				}
				if err = json.NewDecoder(part).Decode(&metadata); err != nil {
					t.Error(err)
					return
				}
				part, err = reader.NextPart()
				if err != nil {
					t.Error(err)
					return
				}
				data, err = io.ReadAll(part)
				if err != nil {
					t.Error(err)
					return
				}
			} else if err := json.NewDecoder(r.Body).Decode(&metadata); err != nil {
				t.Error(err)
				return
			}
			id = strconv.Itoa(len(files) + 1)
			metadata["id"] = id
			metadata["ownedByMe"] = true
			metadata["version"] = "1"
			metadata["capabilities"] = map[string]bool{"canDownload": true}
			if metadata["mimeType"] != drive.FolderMIME {
				sum := md5.Sum(data)
				metadata["md5Checksum"] = hex.EncodeToString(sum[:])
				metadata["size"] = strconv.Itoa(len(data))
				contents[id] = data
			}
			files[id] = metadata
			_ = json.NewEncoder(w).Encode(metadata)
		case "PATCH":
			f := files[id]
			f["trashed"] = true
			_ = json.NewEncoder(w).Encode(f)
		case "GET":
			if r.URL.Path != "/files" {
				f, ok := files[id]
				if !ok {
					w.WriteHeader(404)
					return
				}
				if r.URL.Query().Get("alt") == "media" {
					mediaCalls++
					_, _ = w.Write(contents[id])
					return
				}
				_ = json.NewEncoder(w).Encode(f)
				return
			}
			query := r.URL.Query().Get("q")
			var selected []map[string]any
			for _, f := range files {
				if f["trashed"] == true {
					continue
				}
				parents, _ := f["parents"].([]any)
				if len(parents) > 0 && strings.Contains(query, "'"+parents[0].(string)+"' in parents") {
					selected = append(selected, f)
				}
			}
			sort.Slice(selected, func(i, j int) bool { return selected[i]["id"].(string) < selected[j]["id"].(string) })
			offset, _ := strconv.Atoi(r.URL.Query().Get("pageToken"))
			pageSize, _ := strconv.Atoi(r.URL.Query().Get("pageSize"))
			if pageSize < 1 {
				pageSize = 100
			}
			end := offset + pageSize
			if end > len(selected) {
				end = len(selected)
			}
			next := ""
			if end < len(selected) {
				next = strconv.Itoa(end)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"files": selected[offset:end], "nextPageToken": next, "incompleteSearch": false})
		}
	}))
	defer server.Close()
	execute := func(args ...string) {
		t.Helper()
		root := cli.NewRoot(services(nil, drive.ClientOptions{BaseURL: server.URL, PageSize: 2}))
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&out)
		root.SetArgs(args)
		if err := root.ExecuteContext(context.Background()); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out.String(), "synthetic") {
			t.Fatal("credential leak")
		}
	}
	runDir := filepath.Join(dir, "run")
	manifest := "../../testdata/fixtures/baseline/manifest.json"
	execute("fixture", "seed", "--manifest", manifest, "--run-dir", runDir, "--token-file", token)
	record, err := fixture.LoadRun(runDir)
	if err != nil {
		t.Fatal(err)
	}
	rootID := ""
	for _, o := range record.Objects {
		if o.ParentID == "" {
			rootID = o.RemoteID
		}
	}
	execute("fixture", "inspect", "--run-dir", runDir, "--token-file", token)
	local := filepath.Join(dir, "local")
	args := []string{"sync", "--profile-dir", filepath.Join(dir, "profile"), "--remote-root", rootID, "--local-dir", local, "--token-file", token}
	execute(args...)
	execute("fixture", "verify", "--manifest", manifest, "--local-dir", local)
	count := mediaCalls
	execute(args...)
	if mediaCalls != count {
		t.Fatal("second run downloaded media")
	}
	execute("fixture", "cleanup", "--run-dir", runDir, "--token-file", token)
	for _, f := range files {
		if f["trashed"] != true {
			t.Fatal("fixture cleanup incomplete")
		}
	}
}
