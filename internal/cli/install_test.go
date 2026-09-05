package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSingboxArch(t *testing.T) {
	cases := []struct {
		goarch string
		want   string
		ok     bool
	}{
		{"amd64", "amd64", true},
		{"arm64", "arm64", true},
		{"arm", "armv7", true},
		{"386", "", false},
		{"riscv64", "", false},
	}
	for _, c := range cases {
		got, ok := singboxArch(c.goarch)
		if got != c.want || ok != c.ok {
			t.Errorf("singboxArch(%q) = %q, %v; want %q, %v", c.goarch, got, ok, c.want, c.ok)
		}
	}
}

// fakeSingboxTarball builds a tarball shaped like the real sing-box release:
// sing-box-<ver>-linux-<arch>/sing-box with an extra file to skip.
func fakeSingboxTarball(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	content := []byte("#!/bin/sh\necho sing-box fake\n")
	files := []struct{ name, body string }{
		{"sing-box-1.13.19-linux-arm64/LICENSE", "MIT\n"},
		{"sing-box-1.13.19-linux-arm64/sing-box", string(content)},
	}
	for _, f := range files {
		if err := tw.WriteHeader(&tar.Header{
			Name: f.name, Mode: 0o755, Size: int64(len(f.body)),
		}); err != nil {
			t.Fatalf("write header: %v", err)
		}
		if _, err := tw.Write([]byte(f.body)); err != nil {
			t.Fatalf("write body: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buf.Bytes()
}

func TestLatestSingboxVersion(t *testing.T) {
	orig := singboxReleasesAPI
	t.Cleanup(func() { singboxReleasesAPI = orig })

	t.Run("latest stable", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"tag_name":"v1.14.0"}`))
		}))
		defer srv.Close()
		singboxReleasesAPI = srv.URL

		got, err := latestSingboxVersion()
		if err != nil {
			t.Fatalf("latestSingboxVersion: %v", err)
		}
		if got != "v1.14.0" {
			t.Errorf("got %q, want v1.14.0", got)
		}
	})

	t.Run("http error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "rate limited", http.StatusForbidden)
		}))
		defer srv.Close()
		singboxReleasesAPI = srv.URL

		if _, err := latestSingboxVersion(); err == nil || !strings.Contains(err.Error(), "HTTP 403") {
			t.Errorf("expected HTTP 403 error, got %v", err)
		}
	})

	t.Run("bad json", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`not json`))
		}))
		defer srv.Close()
		singboxReleasesAPI = srv.URL

		if _, err := latestSingboxVersion(); err == nil {
			t.Error("expected decode error")
		}
	})

	t.Run("missing v prefix", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"tag_name":"1.14.0"}`))
		}))
		defer srv.Close()
		singboxReleasesAPI = srv.URL

		if _, err := latestSingboxVersion(); err == nil {
			t.Error("expected error for tag without v prefix")
		}
	})
}

func TestDownloadAndInstallSingbox(t *testing.T) {
	blob := fakeSingboxTarball(t)

	t.Run("success", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write(blob)
		}))
		defer srv.Close()

		dest := t.TempDir()
		var out bytes.Buffer
		if err := downloadAndInstallSingbox(srv.URL+"/sing-box.tar.gz", dest, &out); err != nil {
			t.Fatalf("downloadAndInstallSingbox: %v", err)
		}
		bin := filepath.Join(dest, "sing-box")
		data, err := os.ReadFile(bin)
		if err != nil {
			t.Fatalf("read installed binary: %v", err)
		}
		if !strings.Contains(string(data), "fake") {
			t.Errorf("installed binary content mismatch: %q", data)
		}
		fi, err := os.Stat(bin)
		if err != nil {
			t.Fatalf("stat installed binary: %v", err)
		}
		if fi.Mode().Perm()&0o100 == 0 {
			t.Errorf("installed binary not executable: %v", fi.Mode())
		}
	})

	t.Run("http error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "gone", http.StatusNotFound)
		}))
		defer srv.Close()

		err := downloadAndInstallSingbox(srv.URL, t.TempDir(), &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "HTTP 404") {
			t.Errorf("expected HTTP 404 error, got %v", err)
		}
	})

	t.Run("no sing-box in archive", func(t *testing.T) {
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gz)
		body := "hello"
		if err := tw.WriteHeader(&tar.Header{Name: "README.md", Mode: 0o644, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte(body))
		tw.Close()
		gz.Close()

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write(buf.Bytes())
		}))
		defer srv.Close()

		err := downloadAndInstallSingbox(srv.URL, t.TempDir(), &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "not found") {
			t.Errorf("expected 'not found' error, got %v", err)
		}
	})

	t.Run("corrupt tarball", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("not a gzip"))
		}))
		defer srv.Close()

		err := downloadAndInstallSingbox(srv.URL, t.TempDir(), &bytes.Buffer{})
		if err == nil {
			t.Error("expected error for corrupt tarball")
		}
	})
}
