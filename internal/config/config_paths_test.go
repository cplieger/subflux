package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// --- MediaRoots ---

func TestMediaRoots_returns_configured(t *testing.T) {
	t.Parallel()
	cfg := &Config{MediaRootDirs: []string{"/media", "/data"}}
	got := cfg.MediaRoots()
	if len(got) != 2 || got[0] != "/media" || got[1] != "/data" {
		t.Errorf("MediaRoots() = %v, want [/media /data]", got)
	}
}

func TestMediaRoots_empty(t *testing.T) {
	t.Parallel()
	cfg := &Config{}
	got := cfg.MediaRoots()
	if len(got) != 0 {
		t.Errorf("MediaRoots() = %v, want empty", got)
	}
}

// --- ValidatePath ---

func TestValidatePath(t *testing.T) {
	t.Parallel()

	t.Run("no_roots_allows_all", func(t *testing.T) {
		t.Parallel()
		cfg := &Config{}
		if err := cfg.ValidatePath(context.Background(), "/any/path"); err != nil {
			t.Errorf("ValidatePath() with no roots: unexpected error: %v", err)
		}
	})

	t.Run("rejects_outside_roots", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		cfg := &Config{MediaRootDirs: []string{dir}}
		err := cfg.ValidatePath(context.Background(), "/nonexistent/outside/path")
		if err == nil {
			t.Error("ValidatePath() expected error for path outside roots")
		}
	})

	t.Run("accepts_under_root", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "test.mkv")
		if err := os.WriteFile(path, []byte("test"), 0o644); err != nil {
			t.Fatalf("WriteFile() unexpected error: %v", err)
		}
		cfg := &Config{MediaRootDirs: []string{dir}}
		if err := cfg.ValidatePath(context.Background(), path); err != nil {
			t.Errorf("ValidatePath() unexpected error for valid path: %v", err)
		}
	})

	t.Run("accepts_under_second_root", func(t *testing.T) {
		t.Parallel()
		root1 := t.TempDir()
		root2 := t.TempDir()
		path := filepath.Join(root2, "movie.mkv")
		if err := os.WriteFile(path, []byte("test"), 0o644); err != nil {
			t.Fatalf("WriteFile() unexpected error: %v", err)
		}
		cfg := &Config{MediaRootDirs: []string{root1, root2}}
		if err := cfg.ValidatePath(context.Background(), path); err != nil {
			t.Errorf("ValidatePath() unexpected error for path under second root: %v", err)
		}
	})

	t.Run("nonexistent_root_rejects", func(t *testing.T) {
		t.Parallel()
		cfg := &Config{MediaRootDirs: []string{"/nonexistent_root_abc123"}}
		err := cfg.ValidatePath(context.Background(), "/nonexistent_root_abc123/file.mkv")
		if err == nil {
			t.Error("ValidatePath() expected error for nonexistent root")
		}
	})
}

// --- pathUnderRoot ---

func TestPathUnderRoot_rel_error_returns_false(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("filepath.Rel cross-volume error only reliable on Windows")
	}
	t.Parallel()
	got := pathUnderRoot("/nonexistent_root_xyz_12345", "/nonexistent_root_xyz_12345/file.mkv")
	if got {
		t.Error("pathUnderRoot(nonexistent root) = true, want false")
	}
}

func TestPathUnderRoot_path_escapes_root(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	got := pathUnderRoot(root, "/etc/passwd")
	if got {
		t.Error("pathUnderRoot(escape via /etc/passwd) = true, want false")
	}
}

func TestPathUnderRoot_valid_file(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path := filepath.Join(root, "movie.mkv")
	if err := os.WriteFile(path, []byte("test"), 0o644); err != nil {
		t.Fatalf("WriteFile() unexpected error: %v", err)
	}
	got := pathUnderRoot(root, path)
	if !got {
		t.Error("pathUnderRoot(valid file) = false, want true")
	}
}

func TestPathUnderRoot_symlink_escape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink test unreliable on Windows")
	}
	t.Parallel()
	root := t.TempDir()
	link := filepath.Join(root, "escape")
	if err := os.Symlink("/etc", link); err != nil {
		t.Fatalf("Symlink() unexpected error: %v", err)
	}
	got := pathUnderRoot(root, filepath.Join(root, "escape", "passwd"))
	if got {
		t.Error("pathUnderRoot(symlink escape) = true, want false (symlink-safe containment)")
	}
}

// --- RemoveUnderRoot ---

func TestRemoveUnderRoot_no_roots_refuses(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.srt")
	if err := os.WriteFile(path, []byte("content"), 0o644); err != nil {
		t.Fatalf("WriteFile() unexpected error: %v", err)
	}
	cfg := &Config{}
	err := cfg.RemoveUnderRoot(context.Background(), path)
	if err == nil {
		t.Errorf("RemoveUnderRoot with no media_roots = nil, want refusal error")
	}
	if !errors.Is(err, ErrPathNotAllowed) {
		t.Errorf("RemoveUnderRoot error = %v, want ErrPathNotAllowed", err)
	}
	// File must NOT have been removed — with no roots configured,
	// RemoveUnderRoot now refuses rather than falling back to a
	// scoped-by-media_roots-bypass os.Remove.
	if _, err := os.Stat(path); err != nil {
		t.Errorf("file should still exist after refused RemoveUnderRoot: %v", err)
	}
}

func TestRemoveUnderRoot_file_under_root_removed(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "movie.fr.srt")
	if err := os.WriteFile(path, []byte("content"), 0o644); err != nil {
		t.Fatalf("WriteFile() unexpected error: %v", err)
	}
	cfg := &Config{MediaRootDirs: []string{dir}}
	if err := cfg.RemoveUnderRoot(context.Background(), path); err != nil {
		t.Errorf("RemoveUnderRoot(%q) = %v, want nil", path, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("file still exists after RemoveUnderRoot")
	}
}

func TestRemoveUnderRoot_nonexistent_file_returns_nil(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "gone.srt")
	cfg := &Config{MediaRootDirs: []string{dir}}
	if err := cfg.RemoveUnderRoot(context.Background(), path); err != nil {
		t.Errorf("RemoveUnderRoot(%q) = %v, want nil for nonexistent file", path, err)
	}
}

func TestRemoveUnderRoot_outside_all_roots_returns_error(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	other := t.TempDir()
	path := filepath.Join(other, "escape.srt")
	if err := os.WriteFile(path, []byte("content"), 0o644); err != nil {
		t.Fatalf("WriteFile() unexpected error: %v", err)
	}
	cfg := &Config{MediaRootDirs: []string{root}}
	err := cfg.RemoveUnderRoot(context.Background(), path)
	if err == nil {
		t.Fatal("RemoveUnderRoot() = nil, want error for path outside roots")
	}
	if !errors.Is(err, ErrPathNotAllowed) {
		t.Errorf("RemoveUnderRoot() error = %q, want ErrPathNotAllowed", err)
	}
}
