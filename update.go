package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

const updateRepo = "Dusk-Hermit/todolist-agent"
const updateReleaseRoot = "https://github.com/" + updateRepo + "/releases"
const updateReleasePage = updateReleaseRoot + "/latest"
const updateManifestURL = updateReleasePage + "/download/TodoListAgent-update.json"
const updateAPI = "https://api.github.com/repos/" + updateRepo + "/releases?per_page=30"
const maxUpdateSize = 128 << 20

type githubRelease struct {
	Tag        string         `json:"tag_name"`
	Draft      bool           `json:"draft"`
	Prerelease bool           `json:"prerelease"`
	Assets     []releaseAsset `json:"assets"`
}

type releaseAsset struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	URL    string `json:"browser_download_url"`
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
}

type updateOffer struct {
	Version string
	Asset   releaseAsset
}

type updateView struct {
	Current   string `json:"current"`
	Available string `json:"available,omitempty"`
	Phase     string `json:"phase"`
	Error     string `json:"error,omitempty"`
	RetryAt   int64  `json:"retryAt,omitempty"`
	Auto      bool   `json:"auto"`
}

type updater struct {
	mu        sync.Mutex
	offer     *updateOffer
	phase     string
	failure   string
	checkedAt time.Time
	retryAt   time.Time
	stopAuto  context.CancelFunc
}

func releaseVersion(version string) ([]int, bool) {
	parts := strings.Split(strings.TrimPrefix(version, "v"), ".")
	if len(parts) != 3 {
		return nil, false
	}
	values := make([]int, 3)
	for i, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil || value < 0 || strconv.Itoa(value) != part {
			return nil, false
		}
		values[i] = value
	}
	return values, true
}

func selectUpdate(releases []githubRelease, current, name string) *updateOffer {
	best, ok := releaseVersion(current)
	if !ok {
		return nil
	}
	var offer *updateOffer
	for _, release := range releases {
		v, valid := releaseVersion(release.Tag)
		if release.Draft || release.Prerelease || !valid || !strings.HasPrefix(release.Tag, "v") || slices.Compare(v, best) <= 0 {
			continue
		}
		for _, asset := range release.Assets {
			expected := updateReleaseRoot + "/download/" + release.Tag + "/" + name
			if asset.Name != name || asset.URL != expected || asset.Size <= 0 || asset.Size > maxUpdateSize {
				continue
			}
			if _, err := assetHash(asset.Digest); err != nil {
				continue
			}
			offer = &updateOffer{Version: strings.TrimPrefix(release.Tag, "v"), Asset: asset}
			best = v
			break
		}
	}
	return offer
}

func assetHash(digest string) ([]byte, error) {
	raw, ok := strings.CutPrefix(digest, "sha256:")
	hash, err := hex.DecodeString(raw)
	if !ok || err != nil || len(hash) != sha256.Size {
		return nil, fmt.Errorf("更新缺少有效的 SHA-256")
	}
	return hash, nil
}

func updateClient() *http.Client {
	return &http.Client{CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) >= 5 || r.URL.Scheme != "https" || r.URL.User != nil {
			return fmt.Errorf("更新下载重定向无效")
		}
		switch r.URL.Host {
		case "api.github.com", "github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com":
			return nil
		default:
			return fmt.Errorf("更新下载重定向到了未知主机")
		}
	}}
}

func githubUpdates(ctx context.Context, client *http.Client, apiRetryAt time.Time) ([]githubRelease, error) {
	attempt, cancel := context.WithTimeout(ctx, 7*time.Second)
	release, directErr := directRelease(attempt, client)
	cancel()
	if directErr == nil {
		return []githubRelease{release}, nil
	}
	if apiRetryAt.After(time.Now()) {
		return nil, &githubRateLimit{retryAt: apiRetryAt}
	}
	releases, err := githubAPIUpdates(ctx, client)
	if err != nil {
		return nil, fmt.Errorf("Release 直连不可用（%v），%w", directErr, err)
	}
	return releases, nil
}

func directRelease(ctx context.Context, client *http.Client) (githubRelease, error) {
	var release githubRelease
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, updateManifestURL, nil)
	if err != nil {
		return release, err
	}
	response, err := client.Do(r)
	if err != nil {
		return release, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return release, fmt.Errorf("HTTP %d", response.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&release); err != nil {
		return release, fmt.Errorf("发布清单格式无效")
	}
	if _, valid := releaseVersion(release.Tag); !valid || !strings.HasPrefix(release.Tag, "v") ||
		release.Draft || release.Prerelease || len(release.Assets) == 0 {
		return release, fmt.Errorf("发布清单版本无效")
	}
	for i := range release.Assets {
		asset := &release.Assets[i]
		if _, err := assetHash(asset.Digest); err != nil || asset.Size <= 0 || asset.Size > maxUpdateSize ||
			asset.Name == "" || strings.ContainsAny(asset.Name, "/\\?#") {
			return release, fmt.Errorf("发布清单中的安装包信息无效")
		}
		asset.URL = updateReleaseRoot + "/download/" + release.Tag + "/" + asset.Name
		asset.ID = 0
	}
	return release, nil
}

type githubRateLimit struct{ retryAt time.Time }

func (e *githubRateLimit) Error() string { return "GitHub API 暂时限流" }

func githubAPIUpdates(ctx context.Context, client *http.Client) ([]githubRelease, error) {
	r, err := http.NewRequestWithContext(ctx, "GET", updateAPI, nil)
	if err != nil {
		return nil, err
	}
	r.Header.Set("Accept", "application/vnd.github+json")
	response, err := client.Do(r)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, githubAPIStatusError(response.StatusCode, response.Header, time.Now())
	}
	var releases []githubRelease
	err = json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&releases)
	return releases, err
}

func githubAPIStatusError(status int, headers http.Header, now time.Time) error {
	retry := headers.Get("Retry-After")
	exhausted := headers.Get("X-RateLimit-Remaining") == "0"
	if status != http.StatusTooManyRequests && (status != http.StatusForbidden || (!exhausted && retry == "")) {
		return fmt.Errorf("GitHub API 返回 HTTP %d", status)
	}
	retryAt, _ := http.ParseTime(retry)
	if seconds, err := strconv.ParseInt(retry, 10, 32); err == nil && seconds > 0 {
		retryAt = now.Add(time.Duration(seconds) * time.Second)
	}
	if reset, err := strconv.ParseInt(headers.Get("X-RateLimit-Reset"), 10, 64); exhausted && err == nil {
		if resetAt := time.Unix(reset, 0); resetAt.After(retryAt) {
			retryAt = resetAt
		}
	}
	if !retryAt.After(now) {
		retryAt = now.Add(time.Minute)
	}
	return &githubRateLimit{retryAt: retryAt}
}

func downloadUpdate(ctx context.Context, client *http.Client, asset releaseAsset, destination string) error {
	if asset.ID <= 0 {
		return downloadUpdateFrom(ctx, client, asset, asset.URL, destination)
	}
	attempt, cancel := context.WithTimeout(ctx, 2*time.Minute)
	err := downloadUpdateFrom(attempt, client, asset, asset.URL, destination)
	cancel()
	if err == nil || ctx.Err() != nil {
		return err
	}
	apiURL := "https://api.github.com/repos/" + updateRepo + "/releases/assets/" + strconv.FormatInt(asset.ID, 10)
	if apiErr := downloadUpdateFrom(ctx, client, asset, apiURL, destination); apiErr != nil {
		return fmt.Errorf("Release 直连下载失败（%v），备用下载失败（%v）", err, apiErr)
	}
	return nil
}

func downloadUpdateFrom(ctx context.Context, client *http.Client, asset releaseAsset, source, destination string) error {
	expected, err := assetHash(asset.Digest)
	if err != nil {
		return err
	}
	if asset.Size <= 0 || asset.Size > maxUpdateSize {
		return fmt.Errorf("更新文件大小无效")
	}
	r, err := http.NewRequestWithContext(ctx, "GET", source, nil)
	if err != nil {
		return err
	}
	r.Header.Set("Accept", "application/octet-stream")
	response, err := client.Do(r)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("更新下载返回 HTTP %d", response.StatusCode)
	}
	dir := filepath.Dir(destination)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	part, err := os.CreateTemp(dir, "update-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(part.Name())
	hash := sha256.New()
	size, copyErr := io.Copy(io.MultiWriter(part, hash), io.LimitReader(response.Body, asset.Size+1))
	closeErr := part.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if size != asset.Size || !slices.Equal(hash.Sum(nil), expected) {
		return fmt.Errorf("更新文件校验失败")
	}
	return os.Rename(part.Name(), destination)
}

func (a *App) bindingUpdateState() updateView {
	a.mu.Lock()
	auto := a.state.AutoUpdate
	a.mu.Unlock()
	a.updates.mu.Lock()
	defer a.updates.mu.Unlock()
	view := updateView{Current: appVersion, Phase: a.updates.phase, Error: a.updates.failure, Auto: auto}
	if a.updates.retryAt.After(time.Now()) {
		view.RetryAt = a.updates.retryAt.UnixMilli()
	}
	if a.updates.offer != nil {
		view.Available = a.updates.offer.Version
	}
	return view
}

func (a *App) startUpdateChecks() {
	u := &a.updates
	u.mu.Lock()
	if u.stopAuto != nil {
		u.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	u.stopAuto = cancel
	u.mu.Unlock()
	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for {
			a.checkUpdate(false)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func (a *App) stopUpdateChecks() {
	u := &a.updates
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.stopAuto != nil {
		u.stopAuto()
		u.stopAuto = nil
	}
}

func (a *App) checkUpdate(manual bool) {
	u := &a.updates
	u.mu.Lock()
	now := time.Now()
	interval := 6 * time.Hour
	if u.phase == "error" {
		interval = 15 * time.Minute
	}
	if u.phase == "checking" || u.phase == "downloading" || u.phase == "installing" ||
		(!manual && (u.retryAt.After(now) || now.Sub(u.checkedAt) < interval)) {
		u.mu.Unlock()
		return
	}
	apiRetryAt := u.retryAt
	u.checkedAt = now
	u.phase, u.failure = "checking", ""
	u.mu.Unlock()
	a.emit("update", a.bindingUpdateState())
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		releases, err := githubUpdates(ctx, updateClient(), apiRetryAt)
		u.mu.Lock()
		u.retryAt = time.Time{}
		if err == nil {
			u.offer = selectUpdate(releases, appVersion, updateAssetName)
			u.phase = "checked"
		} else {
			u.phase, u.failure = "error", "检查更新失败："+err.Error()
			var limited *githubRateLimit
			if errors.As(err, &limited) {
				u.retryAt = limited.retryAt
			}
		}
		u.mu.Unlock()
		view := a.bindingUpdateState()
		a.emit("update", view)
		if err == nil && view.Auto && view.Available != "" && platformName == "android" {
			_, _ = a.bindingUpdateInstall()
		}
	}()
}

func (a *App) bindingUpdateAuto(enabled bool) (bool, error) {
	a.mu.Lock()
	next := a.state
	next.AutoUpdate = enabled
	err := a.persistLocked(next)
	a.mu.Unlock()
	if err != nil {
		return false, err
	}
	view := a.bindingUpdateState()
	a.emit("update", view)
	if enabled && view.Available != "" {
		_, _ = a.bindingUpdateInstall()
	}
	return true, nil
}

func (a *App) bindingUpdateCheck() bool {
	a.checkUpdate(true)
	return true
}

func (a *App) bindingUpdateInstall() (bool, error) {
	u := &a.updates
	u.mu.Lock()
	if u.offer == nil || u.phase == "checking" || u.phase == "downloading" || u.phase == "installing" {
		u.mu.Unlock()
		return false, fmt.Errorf("暂无可安装的更新")
	}
	offer := *u.offer
	u.phase, u.failure = "downloading", ""
	u.mu.Unlock()
	a.emit("update", a.bindingUpdateState())
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()
		path := filepath.Join(filepath.Dir(a.dataPath), "updates", offer.Asset.Name)
		if err := downloadUpdate(ctx, updateClient(), offer.Asset, path); err != nil {
			a.updateInstallResult(err.Error())
			return
		}
		u.mu.Lock()
		u.phase = "installing"
		u.mu.Unlock()
		a.emit("update", a.bindingUpdateState())
		if err := a.installUpdate(path); err != nil {
			a.updateInstallResult(err.Error())
		}
	}()
	return true, nil
}

func (a *App) updateInstallResult(reason string) {
	a.updates.mu.Lock()
	a.updates.phase, a.updates.failure = "error", reason
	a.updates.mu.Unlock()
	a.emit("update", a.bindingUpdateState())
}
