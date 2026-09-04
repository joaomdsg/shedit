package blob

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveWritesVerbatimAndHashes(t *testing.T) {
	b := New(t.TempDir())
	saved, err := b.Save("item1", "note.txt", strings.NewReader("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if saved.SHA256 != "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824" || saved.Size != 5 {
		t.Fatalf("%+v", saved)
	}
	if filepath.IsAbs(saved.Rel) || !strings.HasPrefix(saved.Rel, filepath.Join("attachments", "item1")) {
		t.Fatalf("path should be relative to root: %s", saved.Rel)
	}
	f, err := b.Open(saved.Rel)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(f)
	f.Close()
	if string(got) != "hello" {
		t.Fatalf("content %q", got)
	}
}

func TestDataDirectoryCanMove(t *testing.T) {
	from, to := t.TempDir(), filepath.Join(t.TempDir(), "moved")
	saved, _ := New(from).Save("i", "x", strings.NewReader("x"))
	if err := os.Rename(from, to); err != nil {
		t.Fatal(err)
	}
	if _, err := New(to).Open(saved.Rel); err != nil {
		t.Fatalf("blob unreachable after move: %v", err)
	}
}

func TestSaveSanitizesNameAndAvoidsCollisions(t *testing.T) {
	b := New(t.TempDir())
	a, _ := b.Save("i", "../../evil name.txt", strings.NewReader("a"))
	c, _ := b.Save("i", "../../evil name.txt", strings.NewReader("b"))
	if a.Rel == c.Rel {
		t.Fatal("collision")
	}
	if strings.Contains(a.Rel, "..") || !strings.Contains(a.Rel, "evil name") {
		t.Fatalf("bad sanitize %s", a.Rel)
	}
}

func TestSaveFailsLoudlyWhenRootUnwritable(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root ignores permissions")
	}
	root := filepath.Join(t.TempDir(), "ro")
	os.MkdirAll(root, 0o500)
	if _, err := New(root).Save("i", "x", strings.NewReader("x")); err == nil {
		t.Fatal("want error")
	}
}

func TestRemoveItemDeletesDirectory(t *testing.T) {
	b := New(t.TempDir())
	s, _ := b.Save("i", "x", strings.NewReader("x"))
	if err := b.RemoveItem("i"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(b.Abs(s.Rel)); !os.IsNotExist(err) {
		t.Fatal("file still there")
	}
	if err := b.RemoveItem("i"); err != nil {
		t.Fatalf("removing missing dir should be fine: %v", err)
	}
}

func TestRemoveAndOpenRefuseEscapes(t *testing.T) {
	b := New(t.TempDir())
	s, _ := b.Save("i", "x", strings.NewReader("x"))
	if err := b.Remove(s.Rel); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"../outside", "/etc/passwd"} {
		if err := b.Remove(bad); err == nil {
			t.Fatalf("Remove accepted %q", bad)
		}
		if _, err := b.Open(bad); err == nil {
			t.Fatalf("Open accepted %q", bad)
		}
	}
}
