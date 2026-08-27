package main

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// supportedTargets reads the cores directories, so a new platform demands digests.
func supportedTargets(t *testing.T) [][2]string {
	t.Helper()
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(root, "internal", "proxy", "cores"))
	if err != nil {
		t.Fatal(err)
	}
	var targets [][2]string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		targetOS, targetArch, ok := strings.Cut(e.Name(), "-")
		if !ok {
			t.Fatalf("cores directory %q is not <os>-<arch>", e.Name())
		}
		targets = append(targets, [2]string{targetOS, targetArch})
	}
	if len(targets) == 0 {
		t.Fatal("no cores directories found")
	}
	return targets
}

func TestEverySupportedAssetIsPinned(t *testing.T) {
	for _, target := range supportedTargets(t) {
		targetOS, targetArch := target[0], target[1]
		for _, asset := range []string{singboxAssetName(targetOS, targetArch), xrayAssetName(targetOS, targetArch)} {
			if asset == "" {
				t.Errorf("%s/%s: no release asset resolved", targetOS, targetArch)
				continue
			}
			if _, ok := assetDigests[asset]; !ok {
				t.Errorf("%s/%s: asset %q has no pinned digest", targetOS, targetArch, asset)
			}
		}
	}
}

// A leftover key means a version bump updated the constants but not the table.
func TestNoStaleDigests(t *testing.T) {
	used := map[string]bool{}
	for _, target := range supportedTargets(t) {
		used[singboxAssetName(target[0], target[1])] = true
		used[xrayAssetName(target[0], target[1])] = true
	}
	for asset := range assetDigests {
		if !used[asset] {
			t.Errorf("pinned digest for %q belongs to no supported target", asset)
		}
	}
}

func TestDigestsAreWellFormed(t *testing.T) {
	sha256Hex := regexp.MustCompile(`^[0-9a-f]{64}$`)
	for asset, digest := range assetDigests {
		if !sha256Hex.MatchString(digest) {
			t.Errorf("%s: %q is not a lowercase hex SHA-256", asset, digest)
		}
	}
}

func TestExtractZipEntry(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "Xray-macos-arm64-v8a.zip")
	writeTestZip(t, archive, map[string]string{
		"LICENSE":        "not the binary",
		"xray":           "the binary",
		"nested/geoip.d": "data",
	})

	dest := filepath.Join(dir, "xray")
	if err := extractZipEntry(archive, "xray", dest); err != nil {
		t.Fatalf("extracting a present entry failed: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "the binary" {
		t.Fatalf("extracted %q, want the entry's contents", got)
	}

	if err := extractZipEntry(archive, "missing", dest); err == nil {
		t.Fatal("extracting an absent entry returned no error")
	}
}

func writeTestZip(t *testing.T, path string, entries map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = f.Close()
	}()
	zw := zip.NewWriter(f)
	for name, body := range entries {
		w, err := zw.Create(name)
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
}

func TestVerifyDigest(t *testing.T) {
	const asset = "Xray-macos-arm64-v8a.zip"

	if err := verifyDigest(asset, strings.ToUpper(assetDigests[asset])); err != nil {
		t.Errorf("matching digest rejected: %v", err)
	}
	err := verifyDigest(asset, strings.Repeat("0", 64))
	if err == nil || !strings.Contains(err.Error(), "failed its checksum") {
		t.Errorf("mismatch error = %v, want a checksum failure", err)
	}
	err = verifyDigest("sing-box-9.9.9-darwin-arm64.tar.gz", strings.Repeat("0", 64))
	if err == nil || !strings.Contains(err.Error(), "no pinned SHA-256") {
		t.Errorf("unpinned asset error = %v, want a refusal", err)
	}
}

func TestHTTPDownloadHashesWhatItWrites(t *testing.T) {
	body := []byte("pretend this is a release archive")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "asset")
	sum, err := httpDownload(context.Background(), srv.URL, dest)
	if err != nil {
		t.Fatalf("httpDownload: %v", err)
	}
	want := sha256.Sum256(body)
	if sum != hex.EncodeToString(want[:]) {
		t.Errorf("digest = %s, want %s", sum, hex.EncodeToString(want[:]))
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(body) {
		t.Errorf("file on disk does not match the response body")
	}
}

func TestHTTPDownloadRejectsErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	}))
	defer srv.Close()

	if _, err := httpDownload(context.Background(), srv.URL, filepath.Join(t.TempDir(), "asset")); err == nil {
		t.Error("a 404 was accepted as a download")
	}
}
