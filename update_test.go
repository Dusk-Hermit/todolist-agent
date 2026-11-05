package main

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestSelectUpdateUsesNewestCompatibleAsset(t *testing.T) {
	asset := func(tag, name string) releaseAsset {
		return releaseAsset{Name: name, Size: 1234, Digest: fmt.Sprintf("sha256:%064x", 1),
			URL: "https://github.com/Dusk-Hermit/todolist-agent/releases/download/" + tag + "/" + name}
	}
	const apk = "TodoListAgent-android.apk"
	releases := []githubRelease{
		{Tag: "v0.10.0", Assets: []releaseAsset{asset("v0.10.0", apk)}},
		{Tag: "v0.9.0", Assets: []releaseAsset{asset("v0.9.0", apk)}},
		{Tag: "v0.11.0", Draft: true, Assets: []releaseAsset{asset("v0.11.0", apk)}},
		{Tag: "v0.12.0", Assets: []releaseAsset{{Name: apk, URL: "https://example.com/update.apk"}}},
	}
	got := selectUpdate(releases, "0.8.9", apk)
	if got == nil || got.Version != "0.10.0" {
		t.Fatalf("wrong update: %+v", got)
	}
	if selectUpdate(releases, "0.10.0", apk) != nil || selectUpdate(releases, "0.1.0", "TodoListAgent-Setup-win64.exe") != nil {
		t.Fatal("selected an equal, older, or incompatible release")
	}
}

func TestDownloadRejectsCorruptionAndKeepsVerifiedFile(t *testing.T) {
	data := []byte("the published update")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(data)
	}))
	defer server.Close()
	destination := filepath.Join(t.TempDir(), "update.exe")
	asset := releaseAsset{URL: server.URL, Size: int64(len(data)), Digest: fmt.Sprintf("sha256:%x", sha256.Sum256(data))}
	if err := downloadUpdate(t.Context(), server.Client(), asset, destination); err != nil {
		t.Fatal(err)
	}
	asset.Digest = fmt.Sprintf("sha256:%064x", 0)
	if err := downloadUpdate(t.Context(), server.Client(), asset, destination); err == nil {
		t.Fatal("accepted a wrong digest")
	}
	got, err := os.ReadFile(destination)
	if err != nil || string(got) != string(data) {
		t.Fatalf("verified file not retained: %q, %v", got, err)
	}
}
