package main

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

type dshEndpoint struct {
	base   string
	secret []byte
	plugin bool
}

func readDSHConnector() (dshEndpoint, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return dshEndpoint{}, err
	}
	path := filepath.Join(home, ".dsh", "todolist-connector.json")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return dshEndpoint{}, fmt.Errorf("未找到 DSH 插件，请在 DSH 中安装 TodoList 连接插件并重启 DSH")
	}
	if err != nil {
		return dshEndpoint{}, fmt.Errorf("读取 DSH 插件连接信息失败：%w", err)
	}
	var config struct {
		Version int    `json:"version"`
		Port    int    `json:"port"`
		Key     []byte `json:"key"`
	}
	if json.Unmarshal(data, &config) != nil || config.Version != 1 ||
		config.Port < 1024 || config.Port > 65535 || len(config.Key) != 32 {
		return dshEndpoint{}, fmt.Errorf("DSH 插件连接信息无效，请更新并重启 DSH 插件")
	}
	return dshEndpoint{base: fmt.Sprintf("http://127.0.0.1:%d", config.Port), secret: config.Key, plugin: true}, nil
}

func (c *DSHClient) callConnector(ctx context.Context, conn dshEndpoint, method string, args any, timeout time.Duration) (json.RawMessage, int, error) {
	block, err := aes.NewCipher(conn.secret)
	if err != nil {
		return nil, 0, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, 0, err
	}
	body, err := json.Marshal(struct {
		At     int64  `json:"at"`
		Method string `json:"method"`
		Args   any    `json:"args"`
	}{time.Now().UnixMilli(), method, args})
	if err != nil {
		return nil, 0, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, 0, err
	}
	packet := aead.Seal(nonce, nonce, body, []byte("todolist/v1/request"))
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, conn.base+"/todolist/v1/rpc", bytes.NewReader(packet))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, fmt.Errorf("DSH 插件返回 HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if len(data) < aead.NonceSize()+aead.Overhead() {
		return nil, resp.StatusCode, fmt.Errorf("DSH 插件响应无效")
	}
	response, err := aead.Open(nil, data[:aead.NonceSize()], data[aead.NonceSize():], append([]byte("todolist/v1/response:"), nonce...))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("无法验证 DSH 插件身份")
	}
	return decodeDSHResult(response, resp.StatusCode)
}
