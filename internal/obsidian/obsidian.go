package obsidian

import (
	"errors"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Note struct {
	Path    string
	Rel     string
	ModTime time.Time
}

func Recent(dir string, n int) []Note {
	var out []Note
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && p != dir {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(p), ".md") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		out = append(out, Note{Path: p, Rel: strings.TrimSuffix(rel, filepath.Ext(rel)), ModTime: info.ModTime()})
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].ModTime.After(out[j].ModTime) })
	if len(out) > n {
		out = out[:n]
	}
	return out
}

func URI(path string) string {
	q := strings.ReplaceAll(url.QueryEscape(path), "+", "%20")
	return "obsidian://open?path=" + q
}

func isWSL() bool {
	data, err := os.ReadFile("/proc/sys/kernel/osrelease")
	return err == nil && strings.Contains(strings.ToLower(string(data)), "microsoft")
}

func Open(path string) error {
	if _, err := os.Stat(path); err != nil {
		return err
	}
	if isWSL() {
		out, err := exec.Command("wslpath", "-w", path).Output()
		if err != nil {
			return errors.New("wslpath failed: " + err.Error())
		}
		uri := URI(strings.TrimSpace(string(out)))
		return exec.Command("cmd.exe", "/c", "start", "", uri).Start()
	}
	if opener, err := exec.LookPath("xdg-open"); err == nil {
		return exec.Command(opener, URI(path)).Start()
	}
	if opener, err := exec.LookPath("open"); err == nil {
		return exec.Command(opener, URI(path)).Start()
	}
	return errors.New("no way to open Obsidian URIs on this system")
}

var skipDirs = map[string]bool{
	"node_modules": true, "vendor": true, "dist": true, "build": true, "target": true,
	"graphify-out": true, ".git": true, ".next": true, "__pycache__": true,
}

func List(dir string, limit int) []Note {
	var out []Note
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || len(out) >= limit {
			return nil
		}
		if d.IsDir() {
			if p != dir && (strings.HasPrefix(d.Name(), ".") || skipDirs[d.Name()] || IsGenerated(p)) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(p), ".md") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		out = append(out, Note{Path: p, Rel: strings.TrimSuffix(rel, filepath.Ext(rel)), ModTime: info.ModTime()})
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Rel) < strings.ToLower(out[j].Rel) })
	return out
}

func IsGenerated(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".graphify_obsidian_manifest.json"))
	return err == nil
}
