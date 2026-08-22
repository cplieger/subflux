package polling

import (
	"fmt"
	"testing"

	"github.com/cplieger/keyenc"
)

// oldRetryKey is the fmt.Sprintf form retryKey replaced, kept as the
// byte-identity oracle.
func oldRetryKey(source PollSource, entryID int) string {
	return fmt.Sprintf("%s:%d", source, entryID)
}

// TestRetryKeyIsInjectiveAndUnchanged covers the importRetries key.
//
// No tuple pair the old form collapsed exists at this site, and none is
// asserted: source is one of two PollSource constants and entryID is an int, so
// even a hostile source string could not forge a boundary — the int tail pins
// the last ':' and the head absorbs the rest. What the test pins is that the
// shared grammar left the bytes exactly as they were, and that distinct
// (source, entry) pairs stay distinct including when the source carries the
// separator. The property matters because this key indexes the counter that
// holds the poll WATERMARK: two entries sharing it share one attempt budget, so
// the pair would be abandoned after maxImportRetries attempts between them
// instead of each, and clearing one would release the hold the other still
// needs — polling past an import that is still failing.
func TestRetryKeyIsInjectiveAndUnchanged(t *testing.T) {
	t.Parallel()

	type pair struct {
		source  PollSource
		entryID int
	}
	ordinary := []pair{
		{PollSourceSonarr, 0},
		{PollSourceSonarr, 991},
		{PollSourceRadarr, 12345},
	}
	for _, p := range ordinary {
		got := retryKey(p.source, p.entryID)
		if want := oldRetryKey(p.source, p.entryID); got != want {
			t.Errorf("retryKey(%q, %d) = %q, want the unchanged %q", p.source, p.entryID, got, want)
		}
		if keyenc.IsHashed(got) {
			t.Errorf("retryKey(%q, %d) must not be a hashed identity", p.source, p.entryID)
		}
	}

	// The two sources must never share a counter, and neither must two entries.
	if a, b := retryKey(PollSourceSonarr, 7), retryKey(PollSourceRadarr, 7); a == b {
		t.Errorf("sonarr and radarr entry 7 share the retry key %q", a)
	}
	if a, b := retryKey(PollSourceSonarr, 1), retryKey(PollSourceSonarr, 11); a == b {
		t.Errorf("two entry ids share the retry key %q", a)
	}

	// A source carrying the separator cannot merge with a plain one.
	forged := map[string][2]pair{
		"separator in the source": {
			{PollSource("sonarr:1"), 2},
			{PollSourceSonarr, 12},
		},
		"escape character in the source": {
			{PollSource(`sonarr\`), 1},
			{PollSource("sonarr"), 1},
		},
	}
	for name, fp := range forged {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			left := retryKey(fp[0].source, fp[0].entryID)
			right := retryKey(fp[1].source, fp[1].entryID)
			if left == right {
				t.Errorf("distinct poll entries share the retry key %q", left)
			}
		})
	}
}

// TestImportRetryBudget_abandons_the_entry_on_its_last_attempt: an entry is
// retried while its budget lasts and abandoned exactly ON attempt
// maxImportRetries, with its counter cleared. Giving up one attempt late
// would keep holding the poll watermark on an entry already reported as
// abandoned, so the two must agree.
func TestImportRetryBudget_abandons_the_entry_on_its_last_attempt(t *testing.T) {
	t.Parallel()

	p := &Poller{importRetries: make(map[string]int)}
	key := retryKey(PollSourceSonarr, 7)

	for attempt := 1; attempt < maxImportRetries; attempt++ {
		if !p.noteImportFailure(key, "/media/a.mkv") {
			t.Fatalf("noteImportFailure(attempt %d of %d) = false, want a retry",
				attempt, maxImportRetries)
		}
	}
	if p.noteImportFailure(key, "/media/a.mkv") {
		t.Errorf("noteImportFailure(attempt %d of %d) = true, want the entry abandoned",
			maxImportRetries, maxImportRetries)
	}
	if _, held := p.importRetries[key]; held {
		t.Error("retry counter still held after giving up; the watermark cannot move past the entry")
	}
}

// TestImportRetryCounterIsPerEntry drives the counter the key feeds: each
// (source, entry) pair must burn its own retry budget, and clearing one must not
// release another's watermark hold.
func TestImportRetryCounterIsPerEntry(t *testing.T) {
	t.Parallel()

	p := &Poller{importRetries: make(map[string]int)}

	// One failure each for two entries that differ only in the source.
	if !p.noteImportFailure(retryKey(PollSourceSonarr, 5), "/media/a.mkv") {
		t.Fatal("first sonarr failure should be retried")
	}
	if !p.noteImportFailure(retryKey(PollSourceRadarr, 5), "/media/b.mkv") {
		t.Fatal("first radarr failure should be retried")
	}
	if len(p.importRetries) != 2 {
		t.Fatalf("importRetries holds %d counters, want 2 (the two entries merged)", len(p.importRetries))
	}

	// Clearing one must leave the other's counter intact.
	p.clearImportRetry(retryKey(PollSourceSonarr, 5))
	if got := p.importRetries[retryKey(PollSourceRadarr, 5)]; got != 1 {
		t.Errorf("radarr entry counter = %d after clearing the sonarr entry, want 1", got)
	}
}
