package chrome

import (
	"archive/zip"
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func zipOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		h := &zip.FileHeader{Name: name, Method: zip.Deflate}
		h.SetMode(0o755)
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestDownloadedBrowserGet(t *testing.T) {
	archive := zipOf(t, map[string]string{
		"chrome-headless-shell-linux64/chrome-headless-shell": "binary",
		"chrome-headless-shell-linux64/libfoo.so":             "lib",
	})
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	mux.HandleFunc("/index.json", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"channels":{"Stable":{"downloads":{"chrome-headless-shell":[
			{"platform":"mac-arm64","url":"%[1]s/wrong.zip"},
			{"platform":"linux64","url":"%[1]s/shell.zip"}]}}}}`, srv.URL)
	})
	mux.HandleFunc("/shell.zip", func(w http.ResponseWriter, r *http.Request) {
		w.Write(archive)
	})

	b := &downloadedBrowser{
		dir:      filepath.Join(t.TempDir(), "chrome-headless-shell"),
		platform: "linux64",
		index:    srv.URL + "/index.json",
	}
	if b.valid() {
		t.Fatal("valid before download")
	}
	path, err := b.Get()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if path != b.binPath() || !b.valid() {
		t.Fatalf("got %q, want valid %q", path, b.binPath())
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&0o100 == 0 {
		t.Errorf("binary not executable: %v", fi.Mode())
	}
}

func TestUnzipRejectsPathTraversal(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "evil.zip")
	if err := os.WriteFile(archive, zipOf(t, map[string]string{"../evil": "x"}), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "out")
	if err := unzip(archive, dst); err == nil {
		t.Fatal("expected error for path traversal entry")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dst), "evil")); err == nil {
		t.Error("traversal entry was written outside dst")
	}
}

func TestCFTPlatform(t *testing.T) {
	if p, err := cftPlatform("darwin", "arm64"); err != nil || p != "mac-arm64" {
		t.Errorf("darwin/arm64 = %q, %v", p, err)
	}
	if _, err := cftPlatform("freebsd", "amd64"); err == nil {
		t.Error("expected error for unsupported platform")
	}
}
