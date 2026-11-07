package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// findTool looks a program up in PATH and in the folders a desktop app does
// not inherit: Homebrew, ~/.local/bin, the Go bin folder and WinGet.
func findTool(name string) (string, error) {
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	for _, dir := range extraToolDirs() {
		for _, candidate := range []string{name, name + ".exe"} {
			p := filepath.Join(dir, candidate)
			if info, err := os.Stat(p); err == nil && !info.IsDir() {
				return p, nil
			}
		}
	}
	return "", exec.ErrNotFound
}

func homeDir() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return h
}

// extraToolDirs are folders that hold yt-dlp, ffmpeg or sockseek, including
// the ones bundled next to the app.
func extraToolDirs() []string {
	home := homeDir()
	var dirs []string
	switch runtime.GOOS {
	case "darwin":
		dirs = []string{"/opt/homebrew/bin", "/usr/local/bin", filepath.Join(home, ".local", "bin"), filepath.Join(home, "go", "bin")}
	case "windows":
		dirs = []string{
			filepath.Join(os.Getenv("LOCALAPPDATA"), "Microsoft", "WinGet", "Links"),
			filepath.Join(os.Getenv("USERPROFILE"), "scoop", "shims"),
			filepath.Join(home, ".local", "bin"),
		}
	default:
		dirs = []string{"/usr/local/bin", "/usr/bin", filepath.Join(home, ".local", "bin"), filepath.Join(home, "go", "bin")}
	}
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		dirs = append([]string{filepath.Join(exeDir, "bin")}, dirs...)
		if runtime.GOOS == "darwin" {
			dirs = append([]string{filepath.Join(exeDir, "..", "Resources", "bin")}, dirs...)
		}
	}
	return dirs
}

// ensurePath puts the extra folders on PATH, so the tools started by the
// pipeline are found.
func ensurePath() {
	path := os.Getenv("PATH")
	for _, dir := range extraToolDirs() {
		if dir == "" || strings.Contains(path, dir) {
			continue
		}
		path = dir + string(os.PathListSeparator) + path
	}
	_ = os.Setenv("PATH", path)
}
