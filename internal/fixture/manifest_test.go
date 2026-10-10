package fixture

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func baseline(t *testing.T) Verified {
	t.Helper()
	v, err := LoadManifest("../../testdata/fixtures/baseline/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func TestBaselineManifest(t *testing.T) {
	v := baseline(t)
	if len(v.Manifest.Entries) != 9 {
		t.Fatal("wrong baseline membership")
	}
}
func mutateManifest(t *testing.T, change func(*Manifest)) string {
	t.Helper()
	v := baseline(t)
	dir := t.TempDir()
	for _, e := range v.Manifest.Entries {
		if e.Kind == "file" {
			data, err := os.ReadFile(filepath.Join(v.BaseDir, e.Payload))
			if err != nil {
				t.Fatal(err)
			}
			p := filepath.Join(dir, e.Payload)
			if err = os.MkdirAll(filepath.Dir(p), 0700); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(p, data, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	change(&v.Manifest)
	data, err := json.Marshal(v.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "manifest.json")
	if err = os.WriteFile(p, data, 0600); err != nil {
		t.Fatal(err)
	}
	return p
}
func TestManifestInvalidGraph(t *testing.T) {
	for _, change := range []func(*Manifest){func(m *Manifest) { m.Entries[1].Parent = "missing" }, func(m *Manifest) { m.Entries[1].ID = m.Entries[0].ID }, func(m *Manifest) { m.Entries[1].Parent = m.Entries[1].ID }, func(m *Manifest) { m.Entries[1].Name = "../escape" }, func(m *Manifest) { m.Entries[2].Name = m.Entries[1].Name; m.Entries[2].Parent = m.Entries[1].Parent }} {
		if _, err := LoadManifest(mutateManifest(t, change)); err == nil {
			t.Fatal("accepted invalid manifest")
		}
	}
}
func TestManifestUnsafePayload(t *testing.T) {
	p := mutateManifest(t, func(m *Manifest) { m.Entries[3].Payload = "../outside" })
	if _, err := LoadManifest(p); err == nil {
		t.Fatal("accepted escaping payload")
	}
	p = mutateManifest(t, func(m *Manifest) {})
	target := filepath.Join(filepath.Dir(p), "payloads", "hello.txt")
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", target); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadManifest(p); err == nil {
		t.Fatal("accepted symlink")
	}
}
func TestManifestHashes(t *testing.T) {
	p := mutateManifest(t, func(m *Manifest) { m.Entries[3].SHA256 = "bad" })
	if _, err := LoadManifest(p); err == nil {
		t.Fatal("accepted wrong hash")
	}
}
func materialize(t *testing.T, v Verified) string {
	t.Helper()
	dir := t.TempDir()
	paths, err := manifestPaths(v.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range v.Manifest.Entries {
		p := filepath.Join(dir, paths[e.ID])
		if e.Kind == "folder" {
			if err = os.MkdirAll(p, 0700); err != nil {
				t.Fatal(err)
			}
		} else {
			data, err := os.ReadFile(filepath.Join(v.BaseDir, e.Payload))
			if err != nil {
				t.Fatal(err)
			}
			if err = os.MkdirAll(filepath.Dir(p), 0700); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(p, data, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	return dir
}
func TestVerifyExactTree(t *testing.T) {
	v := baseline(t)
	dir := materialize(t, v)
	report, err := VerifyLocal(context.Background(), v, dir)
	if err != nil || report.Files != 5 || report.Directories != 3 {
		t.Fatalf("verify: %+v %v", report, err)
	}
	if err = os.WriteFile(filepath.Join(dir, "unexpected"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = VerifyLocal(context.Background(), v, dir); err == nil {
		t.Fatal("accepted extra file")
	}
	if err = os.Remove(filepath.Join(dir, "unexpected")); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "nested", "hello.txt"), []byte("wrong"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = VerifyLocal(context.Background(), v, dir); err == nil {
		t.Fatal("accepted wrong content")
	}
}
