package main

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
		{Tag: "v0.11.1", Prerelease: true, Assets: []releaseAsset{asset("v0.11.1", apk)}},
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

type updateTransport func(*http.Request) (*http.Response, error)

func (f updateTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestReleaseCheckPrefersDirectManifest(t *testing.T) {
	const apk = "TodoListAgent-android.apk"
	client := &http.Client{Transport: updateTransport(func(r *http.Request) (*http.Response, error) {
		if want := "https://github.com/" + updateRepo + "/releases/latest/download/TodoListAgent-update.json"; r.URL.String() != want {
			t.Fatalf("update checked %s, want %s", r.URL, want)
		}
		body := fmt.Sprintf(`{"tag_name":"v0.4.9","assets":[{"name":%q,"size":12,"digest":"sha256:%064x"}]}`, apk, 1)
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	releases, err := githubUpdates(t.Context(), client, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	offer := selectUpdate(releases, "0.4.8", apk)
	if offer == nil || offer.Asset.URL != "https://github.com/"+updateRepo+"/releases/download/v0.4.9/"+apk {
		t.Fatalf("missing direct update URL: %+v", offer)
	}
}

func TestReleaseCheckFallsBackToAPIAndHonorsRateLimit(t *testing.T) {
	var urls []string
	client := &http.Client{Transport: updateTransport(func(r *http.Request) (*http.Response, error) {
		urls = append(urls, r.URL.String())
		response := &http.Response{StatusCode: http.StatusNotFound, Body: http.NoBody}
		if r.URL.Host == "api.github.com" {
			response.StatusCode = http.StatusForbidden
			response.Header = http.Header{"Retry-After": {"60"}}
		}
		return response, nil
	})}
	_, err := githubUpdates(t.Context(), client, time.Time{})
	var limit *githubRateLimit
	if !errors.As(err, &limit) || time.Until(limit.retryAt) < 50*time.Second {
		t.Fatalf("missing retry deadline: %v", err)
	}
	if strings.Join(urls, " ") != updateManifestURL+" "+updateAPI {
		t.Fatalf("unexpected request order: %v", urls)
	}
	urls = nil
	if _, err := githubUpdates(t.Context(), client, limit.retryAt); !errors.As(err, &limit) || len(urls) != 1 || urls[0] != updateManifestURL {
		t.Fatalf("retry during API cooldown should only use the direct link: %v, %v", urls, err)
	}
	if err := githubAPIStatusError(http.StatusForbidden, nil, time.Now()); errors.As(err, &limit) {
		t.Fatalf("a 403 without limit headers was treated as throttling: %v", err)
	}
}

func TestDownloadPrefersDirectReleaseAsset(t *testing.T) {
	const payload = "release payload"
	const direct = "https://github.com/" + updateRepo + "/releases/download/v0.4.9/TodoListAgent-android.apk"
	client := &http.Client{Transport: updateTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != direct ||
			r.Header.Get("Accept") != "application/octet-stream" {
			t.Fatalf("unexpected download request: %s", r.URL)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(payload))}, nil
	})}
	asset := releaseAsset{ID: 42, URL: direct, Size: int64(len(payload)),
		Digest: fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(payload)))}
	if err := downloadUpdate(t.Context(), client, asset, filepath.Join(t.TempDir(), "update.apk")); err != nil {
		t.Fatal(err)
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
