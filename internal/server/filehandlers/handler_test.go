package filehandlers_test

import (
	"testing"
)

// --- HandleListFiles ---

func TestHandleListFiles_ReturnsSortedFiles(t *testing.T) {
	t.Skip("TODO: returns subtitle files sorted by media_id")
}

func TestHandleListFiles_FiltersByMediaType(t *testing.T) {
	t.Skip("TODO: filters files by episode/movie media type")
}

func TestHandleListFiles_EmptyWhenNoFiles(t *testing.T) {
	t.Skip("TODO: returns empty array when no files exist")
}

// --- HandleDeleteFile ---

func TestHandleDeleteFile_RemovesExternalFile(t *testing.T) {
	t.Skip("TODO: deletes the file from disk and removes DB row")
}

func TestHandleDeleteFile_RejectsEmbeddedFiles(t *testing.T) {
	t.Skip("TODO: returns 400 for embedded source files")
}

func TestHandleDeleteFile_RejectsPathTraversal(t *testing.T) {
	t.Skip("TODO: returns 400 when path contains '..'")
}

func TestHandleDeleteFile_RevertsManualLock(t *testing.T) {
	t.Skip("TODO: reverts manual lock when last manual file deleted")
}

// --- HandleBulkDeleteFiles ---

func TestHandleBulkDeleteFiles_DeletesAllExternal(t *testing.T) {
	t.Skip("TODO: deletes all external files for the given media")
}

func TestHandleBulkDeleteFiles_SkipsEmbedded(t *testing.T) {
	t.Skip("TODO: does not delete embedded files")
}

// --- HandleHistoryIDs ---

func TestHandleHistoryIDs_ReturnsMatchingIDs(t *testing.T) {
	t.Skip("TODO: returns media IDs that have download history")
}

func TestHandleHistoryIDs_FiltersByPrefix(t *testing.T) {
	t.Skip("TODO: filters by prefix query parameter")
}
