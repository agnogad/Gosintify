// Package appstore keeps Gosintify's settings and download history in the
// user's config folder, as plain JSON files.
package appstore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Settings is what the window remembers between runs.
type Settings struct {
	OutputDir    string `json:"outputDir"`
	Bitrate      string `json:"bitrate"`
	Fallback     bool   `json:"fallback"`
	WantSoulseek bool   `json:"wantSoulseek"`
	SoulseekUser string `json:"soulseekUser,omitempty"`
	ClientID     string `json:"clientId,omitempty"`
	Volume       int    `json:"volume"`
}

// HistoryEntry is one finished download, kept by its link.
type HistoryEntry struct {
	Kind    string   `json:"kind"`
	Link    string   `json:"link"`
	Name    string   `json:"name"`
	Artist  string   `json:"artist,omitempty"`
	Cover   string   `json:"cover,omitempty"`
	Tracks  []string `json:"tracks,omitempty"`
	Files   int      `json:"files"`
	Missing int      `json:"missing"`
	Dir     string   `json:"dir"`
	At      int64    `json:"at"`
}

var (
	mu sync.Mutex
)

// Dir returns the config folder, creating it.
func Dir() string {
	if v := strings.TrimSpace(os.Getenv("GOSINTIFY_CONFIG_DIR")); v != "" {
		return v
	}
	base, err := os.UserConfigDir()
	if err != nil {
		base = "."
	}
	return filepath.Join(base, "Gosintify")
}

func path(name string) string {
	dir := Dir()
	_ = os.MkdirAll(dir, 0o755)
	return filepath.Join(dir, name)
}

func defaultOutputDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", "Gosintify")
	}
	return filepath.Join(home, "Music", "Gosintify")
}

// DefaultSettings returns the first-run settings.
func DefaultSettings() Settings {
	return Settings{
		OutputDir:    defaultOutputDir(),
		Bitrate:      "320",
		Fallback:     true,
		Volume:       100,
		WantSoulseek: true,
	}
}

// LoadSettings reads settings.json; missing files give the defaults.
func LoadSettings() Settings {
	s := DefaultSettings()
	raw, err := os.ReadFile(path("settings.json"))
	if err == nil {
		_ = json.Unmarshal(raw, &s)
	}
	if strings.TrimSpace(s.OutputDir) == "" {
		s.OutputDir = defaultOutputDir()
	}
	if s.Bitrate == "" {
		s.Bitrate = "320"
	}
	if s.Volume <= 0 {
		s.Volume = 100
	}
	return s
}

// SaveSettings writes settings.json.
func SaveSettings(s Settings) error {
	mu.Lock()
	defer mu.Unlock()
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path("settings.json"), raw)
}

// History reads history.json, newest first.
func History() []HistoryEntry {
	raw, err := os.ReadFile(path("history.json"))
	if err != nil {
		return []HistoryEntry{}
	}
	var h []HistoryEntry
	if err := json.Unmarshal(raw, &h); err != nil {
		return []HistoryEntry{}
	}
	return h
}

// AddHistory puts e first, replacing an entry with the same link, and keeps
// the newest 200.
func AddHistory(e HistoryEntry) error {
	mu.Lock()
	defer mu.Unlock()
	if e.At == 0 {
		e.At = time.Now().Unix()
	}
	list := History()
	out := []HistoryEntry{e}
	for _, old := range list {
		if old.Link != "" && old.Link == e.Link {
			continue
		}
		out = append(out, old)
	}
	if len(out) > 200 {
		out = out[:200]
	}
	raw, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path("history.json"), raw)
}

// ClearHistory empties history.json.
func ClearHistory() error {
	mu.Lock()
	defer mu.Unlock()
	return writeFileAtomic(path("history.json"), []byte("[]"))
}

// CoversDir is where cover images are cached.
func CoversDir() string {
	dir := filepath.Join(Dir(), "covers")
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

func writeFileAtomic(p string, data []byte) error {
	tmp := p + ".part"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}
