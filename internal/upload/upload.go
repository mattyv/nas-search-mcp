// Package upload is an unverified OS boundary for storing user files.
// Its filename checks and ZIP inspection are tested, not Dafny-proved.
package upload

import (
	"archive/zip"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

const MaxSize int64 = 32 << 20

var (
	ErrInvalid  = errors.New("invalid upload")
	ErrTooLarge = errors.New("upload too large")
	ErrExists   = errors.New("file already exists")
)

type Result struct {
	Name string
	Size int64
}

// Save stores one file in an existing dedicated uploads mount. The caller
// authenticates the request and supplies the configured directory and limit.
func Save(dir, name string, src io.Reader, limit int64) (Result, error) {
	if !filepath.IsAbs(dir) || filepath.Base(filepath.Clean(dir)) != "uploads" {
		return Result{}, ErrInvalid
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return Result{}, ErrInvalid
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return Result{}, err
	}
	defer root.Close()
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		return Result{}, ErrInvalid
	}
	return SaveRoot(root, name, src, limit)
}

// SaveRoot stores one file relative to an already anchored directory. The
// caller must have verified that the root is the intended upload directory.
func SaveRoot(root *os.Root, name string, src io.Reader, limit int64) (Result, error) {
	if root == nil || src == nil || limit <= 0 || limit > MaxSize || !validName(name) {
		return Result{}, ErrInvalid
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return Result{}, err
	}
	tmp := ".upload-" + hex.EncodeToString(nonce[:])
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return Result{}, err
	}
	defer root.Remove(tmp)
	size, copyErr := io.CopyN(f, src, limit+1)
	if copyErr != nil && !errors.Is(copyErr, io.EOF) {
		f.Close()
		return Result{}, copyErr
	}
	if size > limit {
		f.Close()
		return Result{}, ErrTooLarge
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return Result{}, err
	}
	if err := f.Close(); err != nil {
		return Result{}, err
	}
	if err := validateContent(root, tmp, name, size); err != nil {
		return Result{}, err
	}
	if err := root.Link(tmp, name); err != nil {
		if _, statErr := root.Lstat(name); statErr == nil {
			return Result{}, ErrExists
		}
		return Result{}, err
	}
	// Publication cannot be rolled back safely. Cleanup and directory sync are
	// best effort after the link so callers never retry a successful upload.
	_ = root.Remove(tmp)
	if d, err := root.Open("."); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return Result{Name: name, Size: size}, nil
}

func validName(name string) bool {
	if name == "" || len(name) > 255 || strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ") || strings.ContainsAny(name, `/\:`) || !utf8.ValidString(name) {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func validateContent(root *os.Root, tmp, name string, size int64) error {
	f, err := root.Open(tmp)
	if err != nil {
		return err
	}
	defer f.Close()
	var head [512]byte
	n, err := io.ReadFull(f, head[:])
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return err
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".pdf":
		if n < 5 || !bytes.HasPrefix(head[:n], []byte("%PDF-")) {
			return ErrInvalid
		}
	case ".txt", ".md":
		if bytes.IndexByte(head[:n], 0) >= 0 {
			return ErrInvalid
		}
		// Validate the full stream so binary data cannot hide after the prefix.
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return err
		}
		data, err := io.ReadAll(io.LimitReader(f, size+1))
		if err != nil {
			return err
		}
		if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
			return ErrInvalid
		}
	case ".docx":
		if err := validateDocx(f, size); err != nil {
			return err
		}
	}
	return nil
}

func validateDocx(f *os.File, size int64) error {
	z, err := zip.NewReader(f, size)
	if err != nil || len(z.File) > 10000 {
		return ErrInvalid
	}
	var types, document bool
	var total uint64
	for _, part := range z.File {
		name := strings.ToLower(part.Name)
		if part.UncompressedSize64 > 32<<20 || part.UncompressedSize64 > (64<<20)-total {
			return ErrInvalid
		}
		total += part.UncompressedSize64
		if part.Flags&1 != 0 || strings.HasPrefix(name, "/") || strings.Contains(name, `\`) || strings.Contains(name, "vbaproject") || strings.Contains(name, "activex/") || strings.Contains(name, "embeddings/") || strings.Contains(name, "oleobject") {
			return ErrInvalid
		}
		for _, component := range strings.Split(strings.TrimSuffix(name, "/"), "/") {
			if component == "" || component == "." || component == ".." {
				return ErrInvalid
			}
		}
		switch name {
		case "[content_types].xml":
			if types {
				return ErrInvalid
			}
			if part.UncompressedSize64 > 1<<20 {
				return ErrInvalid
			}
			r, err := part.Open()
			if err != nil {
				return ErrInvalid
			}
			b, readErr := io.ReadAll(io.LimitReader(r, 1<<20+1))
			r.Close()
			if readErr != nil || !bytes.Contains(b, []byte("application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml")) {
				return ErrInvalid
			}
			types = true
		case "word/document.xml":
			if document {
				return ErrInvalid
			}
			document = true
		default:
			if strings.HasSuffix(name, ".rels") {
				if part.UncompressedSize64 > 1<<20 {
					return ErrInvalid
				}
				r, err := part.Open()
				if err != nil {
					return ErrInvalid
				}
				b, readErr := io.ReadAll(io.LimitReader(r, 1<<20+1))
				r.Close()
				if readErr != nil || externalRelationship(b) {
					return ErrInvalid
				}
			}
		}
	}
	if !types || !document {
		return fmt.Errorf("%w: not a DOCX", ErrInvalid)
	}
	return nil
}

func externalRelationship(data []byte) bool {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return false
		}
		if err != nil {
			return true
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "Relationship" {
			continue
		}
		for _, attr := range start.Attr {
			if attr.Name.Local == "TargetMode" && strings.EqualFold(attr.Value, "External") {
				return true
			}
		}
	}
}
