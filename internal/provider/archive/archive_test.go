package archive

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/cplieger/go/httpclient"
)

// TestExtractCallsChooseOnce: choose sees every gated member's full name in
// archive order, exactly once, and Extract returns the member at the index
// it answers.
func TestExtractCallsChooseOnce(t *testing.T) {
	t.Parallel()
	big := bytes.Repeat([]byte("a"), maxMember+1)
	tests := []struct {
		name  string
		data  []byte
		names []string
		pick  int
		want  string
	}{
		{
			name: "zip",
			data: makeZip(t,
				zipEntry{name: "Show.S01E01.srt", content: []byte(srt + "one")},
				zipEntry{name: "readme.txt", content: []byte("not a subtitle")},
				zipEntry{name: "Show.S01E02.ass", content: []byte(srt + "two")},
				zipEntry{name: "__MACOSX/._Show.S01E02.ass", content: []byte("resource fork")},
				zipEntry{name: "Show.S01E03.srt", content: big},
				zipEntry{name: "Show.S01E04.vtt", content: []byte(srt + "four")},
			),
			names: []string{"Show.S01E01.srt", "Show.S01E02.ass", "Show.S01E04.vtt"},
			pick:  1,
			want:  srt + "two",
		},
		{
			name:  "rar",
			data:  readFixture(t, "test.rar"),
			names: []string{"Show.S01E01.srt", "Show.S01E02.srt"},
			pick:  1,
			want:  "1\n00:00:01,000 --> 00:00:02,000\nHello from episode 2\n",
		},
		{
			name: "nested_zip",
			data: makeZip(t,
				zipEntry{name: "Season 1/", content: nil, method: 0},
				zipEntry{name: "Season 1/Show.S01E01.srt", content: []byte(srt + "one")},
				zipEntry{name: "Season 2/Extras/Show.S02E01.srt", content: []byte(srt + "s2")},
			),
			names: []string{"Season 1/Show.S01E01.srt", "Season 2/Extras/Show.S02E01.srt"},
			pick:  1,
			want:  srt + "s2",
		},
		{
			name:  "nested_rar",
			data:  readFixture(t, "test_with_dir.rar"),
			names: []string{"dirtest/visible.srt"},
			want:  "1\n00:00:01,000 --> 00:00:02,000\nVisible\n",
		},
		{
			name: "stored_rar",
			data: rar5(false,
				rarFile{name: "a.srt", content: []byte(srt + "a")},
				rarFile{name: "notes.nfo", content: []byte("nfo")},
				rarFile{name: "b.srt", content: []byte(srt + "b")},
			),
			names: []string{"a.srt", "b.srt"},
			pick:  1,
			want:  srt + "b",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := &chooser{pick: tt.pick}
			got, err := Extract(tt.data, c.choose)
			if err != nil || string(got) != tt.want {
				t.Errorf("Extract = %q, %v; want %q", got, err, tt.want)
			}
			if len(c.calls) != 1 || !slices.Equal(c.calls[0], tt.names) {
				t.Errorf("choose calls %q; want one with %q", c.calls, tt.names)
			}
		})
	}
}

// TestExtractRefusesOutsideTheChoice: a choose error or an index outside the
// list extracts nothing, refusing on one line, permanent, with choose's
// cause reachable.
func TestExtractRefusesOutsideTheChoice(t *testing.T) {
	t.Parallel()
	data := makeZip(t, zipEntry{name: "a.srt", content: []byte(srt)}, zipEntry{name: "b.srt", content: []byte(srt)})
	cause := errors.New("order_undetermined\nsecond line")
	for _, tt := range []struct {
		name string
		c    *chooser
	}{
		{"negative", &chooser{pick: -1}},
		{"past_the_list", &chooser{pick: 2}},
		{"choose_error", &chooser{err: cause}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := Extract(data, tt.c.choose)
			if got != nil || err == nil {
				t.Fatalf("Extract = %q, %v; want a refusal", got, err)
			}
			assertRefusal(t, err)
			if tt.c.err != nil && !errors.Is(err, cause) {
				t.Errorf("err %v does not reach choose's error", err)
			}
		})
	}
}

// TestExtractMemberPolicy: Extract never falls back to a member choose did
// not name, and an archive with no gated member refuses without asking.
func TestExtractMemberPolicy(t *testing.T) {
	t.Parallel()
	t.Run("choose_names_none", func(t *testing.T) {
		t.Parallel()
		data := makeZip(t, zipEntry{name: "Show.S01E01.srt", content: []byte(srt)})
		got, err := Extract(data, func([]string) (int, error) { return 0, errors.New("no member for S01E05") })
		if got != nil || err == nil {
			t.Fatalf("Extract = %q, %v; want a refusal", got, err)
		}
		assertRefusal(t, err)
	})
	t.Run("nothing_gated", func(t *testing.T) {
		t.Parallel()
		c := &chooser{}
		data := makeZip(t, zipEntry{name: "readme.txt", content: []byte("x")}, zipEntry{name: "dir/", content: nil})
		got, err := Extract(data, c.choose)
		if got != nil || err == nil || len(c.calls) != 0 {
			t.Fatalf("Extract = %q, %v after %d choose calls; want a refusal and none", got, err, len(c.calls))
		}
		assertRefusal(t, err)
	})
}

// TestGate: the members choose may see.
func TestGate(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name string
		m    member
		want bool
	}{
		{"subtitle", member{name: "sub.srt", packed: 100, unpacked: 200}, true},
		{"nested_subtitle", member{name: "subs/episode.ass", packed: 100, unpacked: 200}, true},
		{"vtt", member{name: "sub.vtt", packed: 100, unpacked: 200}, true},
		{"directory", member{name: "subdir", isDir: true}, false},
		{"other_extension", member{name: "readme.txt", packed: 50, unpacked: 100}, false},
		{"hidden", member{name: ".hidden.srt", packed: 50, unpacked: 100}, false},
		{"hidden_in_directory", member{name: "subs/.hidden.srt", packed: 50, unpacked: 100}, false},
		{"zero_packed_positive_unpacked", member{name: "bomb.srt", unpacked: 1000}, false},
		{"ratio_above_50", member{name: "bomb.srt", packed: 1, unpacked: 51}, false},
		{"ratio_50", member{name: "ok.srt", packed: 1, unpacked: 50}, true},
		{"both_zero", member{name: "empty.srt"}, true},
		{"unknown_size", member{name: "sub.srt", packed: 100, sizeUnknown: true}, false},
		{"negative_size", member{name: "sub.srt", packed: 100, unpacked: -1}, false},
		{"at_the_cap", member{name: "sub.srt", packed: maxMember, unpacked: maxMember}, true},
		{"over_the_cap", member{name: "sub.srt", packed: maxMember, unpacked: maxMember + 1}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := gate(tt.m); got != tt.want {
				t.Errorf("gate(%+v) = %v, want %v", tt.m, got, tt.want)
			}
		})
	}
}

// TestDeclaredSize: a size above math.MaxInt64 is unrepresentable, never
// wrapped negative.
func TestDeclaredSize(t *testing.T) {
	t.Parallel()
	if n, ok := declaredSize(1<<63 - 1); !ok || n != 1<<63-1 {
		t.Errorf("declaredSize(MaxInt64) = %d, %v", n, ok)
	}
	if n, ok := declaredSize(1 << 63); ok || n != 0 {
		t.Errorf("declaredSize(1<<63) = %d, %v; want 0, false", n, ok)
	}
}

// TestRefusalsPermanent: every refusal of an archive that opened, and of a
// compressed stream, is permanent, one line and not ErrNotArchive.
func TestRefusalsPermanent(t *testing.T) {
	t.Parallel()
	unreadable := makeZip(t, zipEntry{name: "a.srt", content: []byte(srt), method: zipLZMA})
	extract := []struct {
		name string
		data []byte
		is   error
	}{
		{"unreadable_method", unreadable, zip.ErrAlgorithm},
		{"empty_member", makeZip(t, zipEntry{name: "a.srt", content: nil, method: 0}), nil},
		{"member_longer_than_declared", zipDeclaring(t, "a.srt", 10, 10, bytes.Repeat([]byte("a"), maxMember+1)), nil},
		{"zip_magic_no_end_record", []byte("PK\x03\x04 truncated"), nil},
		{"rar_magic_unreadable", []byte("Rar!\x1a\x07\x01\x00garbage"), nil},
		{"rar_header_cut", readFixture(t, "test.rar")[:120], nil},
	}
	for _, tt := range extract {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := Extract(tt.data, first)
			if got != nil || err == nil {
				t.Fatalf("Extract = %q, %v; want a refusal", got, err)
			}
			assertRefusal(t, err)
			if tt.is != nil && !errors.Is(err, tt.is) {
				t.Errorf("err %v does not reach %v", err, tt.is)
			}
		})
	}
	t.Run("zip64_count_contradicted", func(t *testing.T) {
		t.Parallel()
		got, err := Extract(zip64Declaring(t, 65_538, zipEntry{name: "a.srt", content: []byte(srt)}, zipEntry{name: "b.srt", content: []byte(srt)}), first)
		if got != nil || err == nil {
			t.Fatalf("Extract = %q, %v; want a refusal", got, err)
		}
		assertRefusal(t, err)
	})
	t.Run("zip64_count_kept", func(t *testing.T) {
		t.Parallel()
		got, err := Extract(zip64Declaring(t, 2, zipEntry{name: "a.srt", content: []byte(srt)}, zipEntry{name: "b.srt", content: []byte(srt)}), first)
		if err != nil || string(got) != srt {
			t.Errorf("Extract = %q, %v; want %q", got, err, srt)
		}
	})
	t.Run("rar_too_many_headers", func(t *testing.T) {
		t.Parallel()
		files := make([]rarFile, maxEntries+1)
		for i := range files {
			files[i] = rarFile{name: fmt.Sprintf("%d.srt", i), content: []byte("x")}
		}
		c := &chooser{}
		got, err := Extract(rar5(false, files...), c.choose)
		if got != nil || err == nil || len(c.calls) != 0 {
			t.Fatalf("Extract = %q, %v after %d choose calls; want a refusal and none", got, err, len(c.calls))
		}
		assertRefusal(t, err)
	})
	t.Run("rar_at_the_header_cap", func(t *testing.T) {
		t.Parallel()
		files := make([]rarFile, maxEntries)
		for i := range files {
			files[i] = rarFile{name: fmt.Sprintf("%d.srt", i), content: fmt.Appendf(nil, "%s%d", srt, i)}
		}
		c := &chooser{pick: maxEntries - 1}
		got, err := Extract(rar5(false, files...), c.choose)
		if want := fmt.Sprintf("%s%d", srt, maxEntries-1); err != nil || string(got) != want {
			t.Errorf("Extract = %q, %v; want %q", got, err, want)
		}
	})
}

func assertRefusal(t *testing.T, err error) {
	t.Helper()
	if !httpclient.IsPermanent(err) {
		t.Errorf("err %v is not permanent", err)
	}
	if errors.Is(err, ErrNotArchive) {
		t.Errorf("err %v is ErrNotArchive", err)
	}
	if strings.ContainsAny(err.Error(), "\r\n") {
		t.Errorf("err %q is not one line", err)
	}
}

// TestNotArchiveKeepsBytes: bytes that are neither zip nor rar answer
// ErrNotArchive, unmarked, and are left as they were.
func TestNotArchiveKeepsBytes(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name string
		data []byte
	}{
		{"srt", []byte(srt)},
		{"ass", []byte("[Script Info]\nTitle: Test\nDialogue: 0,0:00:01.00,0:00:02.00,Default,,0,0,0,,Hello\n")},
		{"binary", bytes.Repeat([]byte{0x00, 0xFF}, 256)},
		{"seven_zip", append([]byte{'7', 'z', 0xBC, 0xAF, 0x27, 0x1C}, bytes.Repeat([]byte{0xFF}, 100)...)},
		{"empty", []byte{}},
		{"nil", nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			in := bytes.Clone(tt.data)
			c := &chooser{}
			got, err := Extract(in, c.choose)
			if got != nil || !errors.Is(err, ErrNotArchive) || httpclient.IsPermanent(err) || len(c.calls) != 0 {
				t.Errorf("Extract = %q, %v (permanent %v, %d choose calls); want ErrNotArchive alone",
					got, err, httpclient.IsPermanent(err), len(c.calls))
			}
			if !bytes.Equal(in, tt.data) {
				t.Errorf("Extract changed its input")
			}
		})
	}
}

// TestExtractPrefixedArchive: an archive with bytes ahead of its signature,
// as a self-extracting one carries, still opens.
func TestExtractPrefixedArchive(t *testing.T) {
	t.Parallel()
	prefix := bytes.Repeat([]byte{0x90}, 512)
	for _, tt := range []struct {
		name string
		data []byte
		want string
	}{
		{"zip", append(bytes.Clone(prefix), makeZip(t, zipEntry{name: "a.srt", content: []byte(srt)})...), srt},
		{"rar", append(bytes.Clone(prefix), readFixture(t, "test_hidden.rar")...), "1\n00:00:01,000 --> 00:00:02,000\nVisible\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := Extract(tt.data, first)
			if err != nil || string(got) != tt.want {
				t.Errorf("Extract = %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}

// zip64Declaring is a zip of files whose end of central directory defers
// to a ZIP64 end record declaring records entries.
func zip64Declaring(t *testing.T, records uint64, files ...zipEntry) []byte {
	t.Helper()
	data := makeZip(t, files...)
	end := bytes.Clone(data[len(data)-zipEndLen:])
	size := binary.LittleEndian.Uint32(end[12:])
	offset := binary.LittleEndian.Uint32(end[16:])
	out := bytes.Clone(data[:len(data)-zipEndLen])
	at := uint64(len(out))
	rec := make([]byte, zip64EndLen)
	binary.LittleEndian.PutUint32(rec, zip64EndSig)
	binary.LittleEndian.PutUint64(rec[4:], zip64EndLen-12)
	binary.LittleEndian.PutUint64(rec[24:], records)
	binary.LittleEndian.PutUint64(rec[32:], records)
	binary.LittleEndian.PutUint64(rec[40:], uint64(size))
	binary.LittleEndian.PutUint64(rec[48:], uint64(offset))
	loc := make([]byte, zip64LocatorLen)
	binary.LittleEndian.PutUint32(loc, zip64LocatorSig)
	binary.LittleEndian.PutUint64(loc[8:], at)
	binary.LittleEndian.PutUint32(loc[16:], 1)
	binary.LittleEndian.PutUint16(end[8:], 0xffff)
	binary.LittleEndian.PutUint16(end[10:], 0xffff)
	out = append(append(out, rec...), loc...)
	return append(out, end...)
}
