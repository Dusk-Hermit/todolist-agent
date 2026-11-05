package main

import (
	"embed"
	"strings"
)

//go:embed web/index.html web/style.css web/app.js assets/icon.ico
var embedded embed.FS

var iconICO []byte

func init() {
	iconICO, _ = embedded.ReadFile("assets/icon.ico")
}

func buildHTML() (string, error) {
	index, err := embedded.ReadFile("web/index.html")
	if err != nil {
		return "", err
	}
	css, err := embedded.ReadFile("web/style.css")
	if err != nil {
		return "", err
	}
	js, err := embedded.ReadFile("web/app.js")
	if err != nil {
		return "", err
	}
	html := string(index)
	html = strings.Replace(html, "/*{{CSS}}*/", string(css), 1)
	html = strings.Replace(html, "/*{{JS}}*/", string(js), 1)
	return html, nil
}
