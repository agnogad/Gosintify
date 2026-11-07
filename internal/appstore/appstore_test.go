package appstore

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSettingsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GOSINTIFY_CONFIG_DIR", dir)

	if got := LoadSettings(); got.Bitrate != "320" || !got.Fallback || got.Volume != 100 {
		t.Fatalf("defaults = %+v", got)
	}
	want := Settings{OutputDir: filepath.Join(dir, "music"), Bitrate: "192", Fallback: false, WantSoulseek: true, SoulseekUser: "someone", Volume: 80}
	if err := SaveSettings(want); err != nil {
		t.Fatal(err)
	}
	got := LoadSettings()
	if got.OutputDir != want.OutputDir || got.Bitrate != "192" || got.Fallback || got.SoulseekUser != "someone" {
		t.Fatalf("loaded = %+v", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "settings.json")); err != nil {
		t.Fatal(err)
	}
}

func TestHistoryKeepsNewestFirstAndReplacesTheSameLink(t *testing.T) {
	t.Setenv("GOSINTIFY_CONFIG_DIR", t.TempDir())

	if list := History(); len(list) != 0 {
		t.Fatalf("new history = %+v", list)
	}
	_ = AddHistory(HistoryEntry{Link: "a", Name: "First", Files: 3})
	_ = AddHistory(HistoryEntry{Link: "b", Name: "Second", Files: 4})
	_ = AddHistory(HistoryEntry{Link: "a", Name: "First again", Files: 5})

	list := History()
	if len(list) != 2 {
		t.Fatalf("history = %+v", list)
	}
	if list[0].Link != "a" || list[0].Name != "First again" {
		t.Errorf("newest = %+v", list[0])
	}
	if list[0].At == 0 {
		t.Error("the entry has no time")
	}
	if err := ClearHistory(); err != nil {
		t.Fatal(err)
	}
	if len(History()) != 0 {
		t.Error("clear left entries")
	}
}

func TestConfigDirHonoursTheEnvironment(t *testing.T) {
	want := filepath.Join(t.TempDir(), "cfg")
	t.Setenv("GOSINTIFY_CONFIG_DIR", want)
	if got := Dir(); got != want {
		t.Errorf("Dir() = %q, want %q", got, want)
	}
}
