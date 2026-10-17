package upload

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func uploadDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "uploads")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestSaveTextAndNoOverwrite(t *testing.T) {
	dir := uploadDir(t)
	r, err := Save(dir, "notes.md", strings.NewReader("hello\n"), 32<<20)
	if err != nil || r != (Result{Name: "notes.md", Size: 6}) {
		t.Fatalf("save: %+v, %v", r, err)
	}
	path := filepath.Join(dir, "notes.md")
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
	if _, err := Save(dir, "notes.md", strings.NewReader("changed"), 32<<20); !errors.Is(err, ErrExists) {
		t.Fatalf("duplicate: %v", err)
	}
	b, _ := os.ReadFile(path)
	if string(b) != "hello\n" {
		t.Fatalf("original changed: %q", b)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("temporary files left: %v", entries)
	}
}

func TestSaveRejectsUnsafeNamesAndTypes(t *testing.T) {
	dir := uploadDir(t)
	for _, name := range []string{"../bad.pdf", "a/b.pdf", `a\b.pdf`, ".hidden.md", "a\n.md", "bad.zip", "bad.exe", "bad.pdf ", ""} {
		if _, err := Save(dir, name, strings.NewReader("hello"), 32<<20); err == nil {
			t.Errorf("accepted name %q", name)
		}
	}
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"fake.pdf", []byte("plain text")},
		{"binary.txt", []byte("a\x00b")},
		{"fake.docx", []byte("PK\x03\x04not a docx")},
		{"fake.md", []byte{0xff, 0xfe}},
	} {
		if _, err := Save(dir, tc.name, bytes.NewReader(tc.data), 32<<20); err == nil {
			t.Errorf("accepted %s", tc.name)
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("files left: %v", entries)
	}
}

func TestSaveBoundAndSymlink(t *testing.T) {
	dir := uploadDir(t)
	if _, err := Save(dir, "large.txt", strings.NewReader("123456"), 5); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("size: %v", err)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("safe"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "linked.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := Save(dir, "linked.txt", strings.NewReader("bad"), 5); !errors.Is(err, ErrExists) {
		t.Fatalf("symlink: %v", err)
	}
	b, _ := os.ReadFile(outside)
	if string(b) != "safe" {
		t.Fatalf("outside changed: %q", b)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("temporary files left: %v", entries)
	}
}

func TestSaveRequiresDedicatedDirectory(t *testing.T) {
	if _, err := Save(t.TempDir(), "ok.txt", strings.NewReader("ok"), 100); err == nil {
		t.Fatal("accepted arbitrary directory")
	}
	parent := t.TempDir()
	actual := filepath.Join(parent, "real")
	if err := os.Mkdir(actual, 0700); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(parent, "uploads")
	if err := os.Symlink(actual, linked); err != nil {
		t.Fatal(err)
	}
	if _, err := Save(linked, "ok.txt", strings.NewReader("ok"), 100); err == nil {
		t.Fatal("accepted symlink directory")
	}
}

func TestSaveDocxStructure(t *testing.T) {
	dir := uploadDir(t)
	makeZip := func(files map[string]string) []byte {
		var buf bytes.Buffer
		z := zip.NewWriter(&buf)
		for name, body := range files {
			f, err := z.Create(name)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.Write([]byte(body)); err != nil {
				t.Fatal(err)
			}
		}
		if err := z.Close(); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}
	valid := makeZip(map[string]string{"[Content_Types].xml": `<Types><Override ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`, "word/document.xml": "<document/>"})
	if _, err := Save(dir, "good.docx", bytes.NewReader(valid), 32<<20); err != nil {
		t.Fatalf("valid docx: %v", err)
	}
	archive := makeZip(map[string]string{"file.txt": "hello"})
	if _, err := Save(dir, "archive.docx", bytes.NewReader(archive), 32<<20); err == nil {
		t.Fatal("accepted generic zip")
	}
	active := makeZip(map[string]string{"[Content_Types].xml": `<Types><Override ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`, "word/document.xml": "<document/>", "word/vbaProject.bin": "macro"})
	if _, err := Save(dir, "active.docx", bytes.NewReader(active), 32<<20); err == nil {
		t.Fatal("accepted macro")
	}
	traversal := makeZip(map[string]string{
		"[Content_Types].xml": `<Types><Override ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`,
		"word/document.xml":   "<document/>",
		"word/..":             "unsafe",
	})
	if _, err := Save(dir, "traversal.docx", bytes.NewReader(traversal), 32<<20); err == nil {
		t.Fatal("accepted parent path component in ZIP member")
	}
	for _, relationship := range []string{
		`<Relationships><Relationship TargetMode='External' Target='http://127.0.0.1/'/></Relationships>`,
		"<Relationships><Relationship TargetMode = \"External\" Target=\"http://127.0.0.1/\"/></Relationships>",
	} {
		external := makeZip(map[string]string{
			"[Content_Types].xml":          `<Types><Override ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`,
			"word/document.xml":            "<document/>",
			"word/_rels/document.xml.rels": relationship,
		})
		if _, err := Save(dir, "external.docx", bytes.NewReader(external), 32<<20); err == nil {
			t.Fatalf("accepted external relationship %q", relationship)
		}
	}
}

func TestSaveRejectsOversizedDocxPart(t *testing.T) {
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	for _, name := range []string{"[Content_Types].xml", "word/document.xml", "word/large.bin"} {
		f, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		switch name {
		case "[Content_Types].xml":
			_, err = io.WriteString(f, `<Types><Override ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`)
		case "word/document.xml":
			_, err = io.WriteString(f, "<document/>")
		default:
			_, err = io.CopyN(f, zeroReader{}, (32<<20)+1)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	dir := uploadDir(t)
	if _, err := Save(dir, "oversized.docx", bytes.NewReader(buf.Bytes()), 32<<20); err == nil {
		t.Fatal("accepted oversized uncompressed ZIP member")
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }
