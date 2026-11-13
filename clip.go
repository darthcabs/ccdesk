package main

// Image clipboard via wl-clipboard (Gio's own clipboard is text-only).

import (
	"bytes"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func runQuiet(name string, args ...string) {
	cmd := exec.Command(name, args...)
	cmd.Run()
}

func runOut(timeout time.Duration, name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return out.Bytes(), err
	case <-time.After(timeout):
		cmd.Process.Kill()
		return nil, os.ErrDeadlineExceeded
	}
}

var imageExts = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
	".gif": "image/gif", ".webp": "image/webp",
}

// clipboardImage returns an image from the Wayland clipboard, if it holds one
// (either raw image data, or a copied image file from a file manager).
func clipboardImage() (mime string, data []byte, ok bool) {
	types, err := runOut(time.Second, "wl-paste", "--list-types")
	if err != nil {
		return "", nil, false
	}
	var uriList bool
	for _, t := range strings.Split(string(types), "\n") {
		t = strings.TrimSpace(t)
		if t == "text/uri-list" {
			uriList = true
		}
	}
	for _, pref := range []string{"image/png", "image/jpeg", "image/webp", "image/gif"} {
		for _, t := range strings.Split(string(types), "\n") {
			if strings.TrimSpace(t) == pref {
				b, err := runOut(5*time.Second, "wl-paste", "--no-newline", "--type", pref)
				if err == nil && len(b) > 0 {
					return pref, b, true
				}
			}
		}
	}
	if uriList {
		b, err := runOut(time.Second, "wl-paste", "--no-newline", "--type", "text/uri-list")
		if err == nil {
			for _, line := range strings.Split(string(b), "\n") {
				line = strings.TrimSpace(line)
				if !strings.HasPrefix(line, "file://") {
					continue
				}
				u, err := url.Parse(line)
				if err != nil {
					continue
				}
				if m, ok := imageExts[strings.ToLower(filepath.Ext(u.Path))]; ok {
					if data, err := os.ReadFile(u.Path); err == nil {
						return m, data, true
					}
				}
			}
		}
	}
	return "", nil, false
}

func copyImage(mime string, data []byte) error {
	cmd := exec.Command("wl-copy", "--type", mime)
	cmd.Stdin = bytes.NewReader(data)
	return cmd.Run()
}

func copyText(s string) error {
	cmd := exec.Command("wl-copy")
	cmd.Stdin = strings.NewReader(s)
	return cmd.Run()
}
