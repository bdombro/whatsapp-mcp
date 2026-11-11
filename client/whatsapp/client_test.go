package whatsapp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Verifies sender-chosen document titles become flat, safe filenames that stay inside the downloads dir.
func TestDownloadFileName(t *testing.T) {
	cases := []struct{ filename, id, media, want string }{
		{"report.pdf", "ABCDEFGHIJK", "document", "report_ABCDEFGH.pdf"},
		{"../../../../Library/LaunchAgents/x.plist", "ABCDEFGHIJK", "document", "x_ABCDEFGH.plist"},
		{`..\..\evil.sh`, "ABCDEFGHIJK", "document", "evil_ABCDEFGH.sh"},
		{".zshrc", "ABCDEFGHIJK", "document", "zshrc_ABCDEFGH.bin"},
		{"..", "ABCDEFGHIJK", "document", "media_ABCDEFGH.bin"},
		{"", "AB", "image", "media_AB.jpg"},
		{"a\x00b|c.txt", "ABCDEFGHIJK", "document", "a_b_c_ABCDEFGH.txt"},
		{"photo", "../../x", "image", "photo_______x.jpg"},
	}
	for _, c := range cases {
		got := downloadFileName(c.filename, c.id, c.media)
		if got != c.want {
			t.Errorf("downloadFileName(%q, %q) = %q, want %q", c.filename, c.id, got, c.want)
		}
		if strings.ContainsAny(got, `/\`) || filepath.Base(got) != got {
			t.Errorf("downloadFileName(%q) = %q contains a path separator", c.filename, got)
		}
	}
	long := downloadFileName(strings.Repeat("é", 300)+".pdf", "ABCDEFGHIJK", "document")
	if len([]rune(long)) > 120 {
		t.Errorf("long name not truncated: %d runes", len([]rune(long)))
	}
}

// Verifies send tools only read regular files under home or temp dirs, and never hidden paths or ~/Library.
func TestCheckSendablePath(t *testing.T) {
	dir := t.TempDir()
	ok := filepath.Join(dir, "photo.jpg")
	hiddenDir := filepath.Join(dir, ".secret")
	hidden := filepath.Join(hiddenDir, "key.jpg")
	hiddenFile := filepath.Join(dir, ".env")
	link := filepath.Join(dir, "link.jpg")
	if err := os.MkdirAll(hiddenDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{ok, hidden, hiddenFile} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(hidden, link); err != nil {
		t.Fatal(err)
	}
	dotLink := filepath.Join(dir, ".zshrc")
	if err := os.Symlink(ok, dotLink); err != nil {
		t.Fatal(err)
	}

	if _, err := checkSendablePath(ok); err != nil {
		t.Fatalf("expected %s to be sendable: %v", ok, err)
	}
	for _, p := range []string{
		hidden,
		hiddenFile,
		link,    // symlink into a hidden folder
		dotLink, // hidden symlink to a visible file (like ~/.zshrc -> a dotfiles repo)
		dir,     // not a regular file
		"relative/photo.jpg",
		"/etc/hosts",
		filepath.Join(dir, "missing.jpg"),
	} {
		if _, err := checkSendablePath(p); err == nil {
			t.Errorf("expected %s to be refused", p)
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		if prefs := filepath.Join(home, "Library", "Preferences"); dirExists(prefs) {
			entries, _ := os.ReadDir(prefs)
			for _, e := range entries {
				if e.Type().IsRegular() {
					if _, err := checkSendablePath(filepath.Join(prefs, e.Name())); err == nil {
						t.Errorf("expected ~/Library file to be refused")
					}
					break
				}
			}
		}
	}
}

// dirExists reports whether path is an existing directory.
func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
