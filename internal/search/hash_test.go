package search

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"pgregory.net/rapid"
)

func TestHashFile_known_content(t *testing.T) {
	t.Parallel()

	// Create a file of exactly 128KB (minimum size) filled with zeros.
	// Hash = filesize + sum(first 64KB as uint64) + sum(last 64KB as uint64)
	// For all-zero file: each uint64 is 0, so hash = 131072 (filesize only).
	dir := t.TempDir()
	path := filepath.Join(dir, "zeros.bin")
	data := make([]byte, hashBlockSize*2)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	hash, size, err := hashFile(context.Background(), path)
	if err != nil {
		t.Fatalf("hashFile(zeros) unexpected error: %v", err)
	}
	if size != hashBlockSize*2 {
		t.Errorf("hashFile(zeros) size = %d, want %d", size, hashBlockSize*2)
	}
	if hash != "0000000000020000" {
		t.Errorf("hashFile(zeros) = %q, want %q", hash, "0000000000020000")
	}
}

func TestHashFile_nonzero_content(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "one.bin")
	data := make([]byte, hashBlockSize*2)
	data[0] = 1

	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	hash, size, err := hashFile(context.Background(), path)
	if err != nil {
		t.Fatalf("hashFile(one) unexpected error: %v", err)
	}
	if size != hashBlockSize*2 {
		t.Errorf("hashFile(one) size = %d, want %d", size, hashBlockSize*2)
	}
	if hash != "0000000000020001" {
		t.Errorf("hashFile(one) = %q, want %q", hash, "0000000000020001")
	}
}

func TestHashFile_tail_contributes(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "tail.bin")
	data := make([]byte, hashBlockSize*3)
	data[hashBlockSize*2] = 2

	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	hash, size, err := hashFile(context.Background(), path)
	if err != nil {
		t.Fatalf("hashFile(tail) unexpected error: %v", err)
	}
	if size != hashBlockSize*3 {
		t.Errorf("hashFile(tail) size = %d, want %d", size, hashBlockSize*3)
	}
	if hash != "0000000000030002" {
		t.Errorf("hashFile(tail) = %q, want %q", hash, "0000000000030002")
	}
}

func TestHashFile_overlapping_head_tail(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "overlap.bin")
	data := make([]byte, hashBlockSize*2)
	binary.LittleEndian.PutUint64(data[0:8], 5)
	binary.LittleEndian.PutUint64(data[hashBlockSize*2-8:], 3)

	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	hash, size, err := hashFile(context.Background(), path)
	if err != nil {
		t.Fatalf("hashFile(overlap) unexpected error: %v", err)
	}
	if size != hashBlockSize*2 {
		t.Errorf("hashFile(overlap) size = %d, want %d", size, hashBlockSize*2)
	}
	if hash != "0000000000020008" {
		t.Errorf("hashFile(overlap) = %q, want %q", hash, "0000000000020008")
	}
}

func TestHashFile_file_too_small(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "small.bin")
	data := make([]byte, hashBlockSize*2-1)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, size, err := hashFile(context.Background(), path)
	if err == nil {
		t.Fatal("hashFile(too small) expected error, got nil")
	}
	if size != hashBlockSize*2-1 {
		t.Errorf("hashFile(too small) size = %d, want %d", size, hashBlockSize*2-1)
	}
}

func TestHashFile_empty_file(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "empty.bin")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, size, err := hashFile(context.Background(), path)
	if err == nil {
		t.Fatal("hashFile(empty) expected error, got nil")
	}
	if size != 0 {
		t.Errorf("hashFile(empty) size = %d, want 0", size)
	}
}

func TestHashFile_nonexistent(t *testing.T) {
	t.Parallel()

	_, _, err := hashFile(context.Background(), "/nonexistent/path/to/file.bin")
	if err == nil {
		t.Fatal("hashFile(nonexistent) expected error, got nil")
	}
}

func TestHashFile_uint64_overflow_wraps(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "overflow.bin")
	data := make([]byte, hashBlockSize*2)
	for i := range hashBlockSize {
		data[i] = 0xFF
	}

	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	hash, _, err := hashFile(context.Background(), path)
	if err != nil {
		t.Fatalf("hashFile(overflow) unexpected error: %v", err)
	}

	if hash != "000000000001e000" {
		t.Errorf("hashFile(overflow) = %q, want %q", hash, "000000000001e000")
	}
}

func TestHashFile_large_file_head_tail_independent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "large.bin")
	data := make([]byte, hashBlockSize*4)
	binary.LittleEndian.PutUint64(data[0:8], 10)
	binary.LittleEndian.PutUint64(data[hashBlockSize*3:hashBlockSize*3+8], 20)

	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	hash, size, err := hashFile(context.Background(), path)
	if err != nil {
		t.Fatalf("hashFile(large) unexpected error: %v", err)
	}
	if size != hashBlockSize*4 {
		t.Errorf("hashFile(large) size = %d, want %d", size, hashBlockSize*4)
	}
	if hash != "000000000004001e" {
		t.Errorf("hashFile(large) = %q, want %q", hash, "000000000004001e")
	}
}

func TestHashFile_middle_bytes_irrelevant(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		// Generate a file with 3 blocks: head (64KB) + middle (64KB) + tail (64KB).
		// Vary the middle block; hash should remain constant.
		head := rapid.SliceOfN(rapid.Byte(), hashBlockSize, hashBlockSize).Draw(t, "head")
		tail := rapid.SliceOfN(rapid.Byte(), hashBlockSize, hashBlockSize).Draw(t, "tail")
		middle1 := rapid.SliceOfN(rapid.Byte(), hashBlockSize, hashBlockSize).Draw(t, "middle1")
		middle2 := rapid.SliceOfN(rapid.Byte(), hashBlockSize, hashBlockSize).Draw(t, "middle2")

		dir, err := os.MkdirTemp("", "hash-pbt-*")
		if err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(dir)

		file1 := make([]byte, 0, hashBlockSize*3)
		file1 = append(file1, head...)
		file1 = append(file1, middle1...)
		file1 = append(file1, tail...)

		file2 := make([]byte, 0, hashBlockSize*3)
		file2 = append(file2, head...)
		file2 = append(file2, middle2...)
		file2 = append(file2, tail...)

		path1 := filepath.Join(dir, "f1.bin")
		path2 := filepath.Join(dir, "f2.bin")
		if err := os.WriteFile(path1, file1, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path2, file2, 0o644); err != nil {
			t.Fatal(err)
		}

		hash1, size1, err1 := hashFile(context.Background(), path1)
		hash2, size2, err2 := hashFile(context.Background(), path2)
		if err1 != nil {
			t.Fatal(err1)
		}
		if err2 != nil {
			t.Fatal(err2)
		}
		if size1 != size2 {
			t.Fatalf("hashFile sizes differ: %d vs %d", size1, size2)
		}
		if hash1 != hash2 {
			t.Fatalf("hashFile with different middle bytes produced different hashes: %q vs %q", hash1, hash2)
		}

		// Determinism: hash same file twice.
		hash1b, _, err := hashFile(context.Background(), path1)
		if err != nil {
			t.Fatal(err)
		}
		if hash1 != hash1b {
			t.Fatalf("hashFile not deterministic: %q vs %q", hash1, hash1b)
		}
	})
}
