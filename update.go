package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
const updateAPI = "https://api.github.com/repos/" + updateRepo + "/releases?per_page=30"
const maxUpdateSize = 128 << 20

type githubRelease struct {
	Tag    string         `json:"tag_name"`
	Draft  bool           `json:"draft"`
	Assets []releaseAsset `json:"assets"`
}

type releaseAsset struct {
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
	Auto      bool   `json:"auto"`
}

type updater struct {
	mu        sync.Mutex
	offer     *updateOffer
	phase     string
	failure   string
	checkedAt time.Time
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
		if release.Draft || !valid || !strings.HasPrefix(release.Tag, "v") || slices.Compare(v, best) <= 0 {
			continue
		}
		for _, asset := range release.Assets {
			expected := "https://github.com/" + updateRepo + "/releases/download/" + release.Tag + "/" + name
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
		case "github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com":
			return nil
		default:
			return fmt.Errorf("更新下载重定向到了未知主机")
		}
	}}
}

func githubUpdates(ctx context.Context, client *http.Client) ([]githubRelease, error) {
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
		return nil, fmt.Errorf("GitHub 返回 HTTP %d", response.StatusCode)
	}
	var releases []githubRelease
	err = json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&releases)
	return releases, err
}

func downloadUpdate(ctx context.Context, client *http.Client, asset releaseAsset, destination string) error {
	expected, err := assetHash(asset.Digest)
	if err != nil {
		return err
	}
	if asset.Size <= 0 || asset.Size > maxUpdateSize {
		return fmt.Errorf("更新文件大小无效")
	}
	r, err := http.NewRequestWithContext(ctx, "GET", asset.URL, nil)
	if err != nil {
		return err
	}
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
	interval := 6 * time.Hour
	if u.phase == "error" {
		interval = 15 * time.Minute
	}
	if u.phase == "checking" || u.phase == "downloading" || u.phase == "installing" ||
		(!manual && time.Since(u.checkedAt) < interval) {
		u.mu.Unlock()
		return
	}
	u.checkedAt = time.Now()
	u.phase, u.failure = "checking", ""
	u.mu.Unlock()
	a.emit("update", a.bindingUpdateState())
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		releases, err := githubUpdates(ctx, updateClient())
		u.mu.Lock()
		if err == nil {
			u.offer = selectUpdate(releases, appVersion, updateAssetName)
			u.phase = "checked"
		} else {
			u.phase, u.failure = "error", "检查更新失败："+err.Error()
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
