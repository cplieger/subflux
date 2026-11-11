package search

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// hashOf hashes a file holding data.
func hashOf(t *testing.T, data []byte) (uint64, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "video.mkv")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	return videoHash(f)
}

func TestVideoHash(t *testing.T) {
	overlap := make([]byte, 2*hashBlock)
	binary.LittleEndian.PutUint64(overlap, 5)
	binary.LittleEndian.PutUint64(overlap[2*hashBlock-8:], 3)
	tail := make([]byte, 3*hashBlock)
	tail[2*hashBlock] = 2
	head := make([]byte, 2*hashBlock)
	head[0] = 1
	cases := []struct {
		name string
		data []byte
		want uint64
	}{
		{"zeros: the size alone", make([]byte, 2*hashBlock), 0x20000},
		{"a head byte", head, 0x20001},
		{"a tail byte", tail, 0x30002},
		{"both ends", overlap, 0x20008},
	}
	for _, c := range cases {
		h, err := hashOf(t, c.data)
		if err != nil || h != c.want {
			t.Errorf("%s: hash = %#x, %v; want %#x", c.name, h, err, c.want)
		}
	}
	if h, err := hashOf(t, make([]byte, 2*hashBlock-1)); !errors.Is(err, errHashTooSmall) || h != 0 {
		t.Errorf("a file under 128 KiB hashed %#x, %v; want errHashTooSmall", h, err)
	}
}

func TestVideoHashLeavesOffset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "video.mkv")
	if err := os.WriteFile(path, make([]byte, 3*hashBlock), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err := videoHash(f); err != nil {
		t.Fatal(err)
	}
	if off, err := f.Seek(0, 1); err != nil || off != 0 {
		t.Errorf("offset after hashing = %d, %v; want 0, so a probe of the same descriptor reads from the start", off, err)
	}
}
