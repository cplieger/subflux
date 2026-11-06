// Package subtitlefile is the subtitle file name grammar,
// <stem>.<language>[.sdh][.forced][.<N>].<ext>, and which video in a folder
// owns a name. The names land on SMB and NFS shares that media servers read,
// so the segment order is a compatibility contract: Parse must read back
// every name Build writes.
package subtitlefile

import (
	"errors"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/subtitleext"
	"github.com/cplieger/subflux/internal/timing"
)

// Name is what a subtitle file name says beside its video's stem.
type Name struct {
	Tag     language.Tag
	Ordinal int // 0: unnumbered; 1 to 999,999,999
	Flags   language.Flags
	Ext     Ext
}

// Ext is a subtitle extension the inventory reads. ExtVobSub (.sub) is on
// disk only: no writer produces it and timing.ParseCues refuses its payload.
type Ext uint8

// The extensions; the zero Ext is none.
const (
	ExtSRT Ext = iota + 1
	ExtASS
	ExtSSA
	ExtVobSub
)

// maxOrdinal is the largest ordinal Parse reads back.
const maxOrdinal = 999_999_999

var extText = [...]string{ExtSRT: ".srt", ExtASS: ".ass", ExtSSA: ".ssa", ExtVobSub: ".sub"}

// String is the extension with its dot; "" for the zero or an unknown Ext.
func (e Ext) String() string {
	if int(e) >= len(extText) {
		return ""
	}
	return extText[e]
}

// ExtOf is the extension a writer uses for f; the zero Ext for the zero
// Format.
func ExtOf(f timing.Format) Ext {
	switch f {
	case timing.SRT:
		return ExtSRT
	case timing.ASS:
		return ExtASS
	case timing.SSA:
		return ExtSSA
	}
	return 0
}

// The refusals of Build.
var (
	ErrZeroTag       = errors.New("subtitlefile: a name needs a language")
	ErrTagUnreadable = errors.New("subtitlefile: the language segment reads back as another language")
	ErrOrdinal       = errors.New("subtitlefile: an ordinal is from 1 to 999,999,999")
	ErrNotWritable   = errors.New("subtitlefile: no writer produces this extension")
)

// Build is the name of n beside a video whose stem is stem. It writes HI as
// .sdh (Jellyfin reads .hi after a language it does not know as Hindi) and
// both flags when both are set. Parse(stem, name) is n for every name Build
// returns: a Tag whose segment reads back as another language (language
// FilenameTag "cht" is Cholon but reads as Traditional Chinese) is
// ErrTagUnreadable.
func Build(stem string, n Name) (string, error) {
	if n.Tag == (language.Tag{}) {
		return "", ErrZeroTag
	}
	seg := language.FilenameTag(n.Tag)
	if back, ok := language.ReadFilename(seg); !ok || back != n.Tag {
		return "", ErrTagUnreadable
	}
	if n.Ordinal < 0 || n.Ordinal > maxOrdinal {
		return "", ErrOrdinal
	}
	if !subtitleext.WriterOutput(n.Ext.String()) {
		return "", ErrNotWritable
	}
	var b strings.Builder
	b.WriteString(stem)
	b.WriteString(".")
	b.WriteString(seg)
	if n.Flags.HI {
		b.WriteString(".sdh")
	}
	if n.Flags.Forced {
		b.WriteString(".forced")
	}
	if n.Ordinal > 0 {
		b.WriteString(".")
		b.WriteString(strconv.Itoa(n.Ordinal))
	}
	b.WriteString(n.Ext.String())
	return b.String(), nil
}

// Parse reads name as a subtitle of the video whose stem is stem: the first
// segment after "<stem>." is always the language (language.ReadFilename, so
// "sdh" there is Southern Kurdish and "hi" Hindi), then hi, sdh or cc as HI
// and forced or foreign as Forced in either order, then an ordinal from 1
// with no leading zero, then an extension subtitleext.OnDisk accepts.
// "<stem>.srt" is the zero Tag. False: name is not this grammar.
func Parse(stem, name string) (Name, bool) {
	tail, ok := strings.CutPrefix(name, stem)
	if !ok || !strings.HasPrefix(tail, ".") || !subtitleext.OnDisk(tail) {
		return Name{}, false
	}
	ext := filepath.Ext(tail)
	n := Name{Ext: extFromText(strings.ToLower(ext))}
	body := tail[:len(tail)-len(ext)]
	if body == "" {
		return n, true
	}
	segs := strings.Split(body[1:], ".")
	if n.Tag, ok = language.ReadFilename(segs[0]); !ok || n.Tag == (language.Tag{}) {
		return Name{}, false
	}
	segs = segs[1:]
	if k := len(segs); k > 0 {
		if ord, isOrd := ordinal(segs[k-1]); isOrd {
			n.Ordinal = ord
			segs = segs[:k-1]
		}
	}
	if n.Flags, ok = readFlags(segs); !ok {
		return Name{}, false
	}
	return n, true
}

// readFlags reads each flag at most once, in either order.
func readFlags(segs []string) (language.Flags, bool) {
	var f language.Flags
	for _, s := range segs {
		switch strings.ToLower(s) {
		case "hi", "sdh", "cc":
			if f.HI {
				return f, false
			}
			f.HI = true
		case "forced", "foreign":
			if f.Forced {
				return f, false
			}
			f.Forced = true
		default:
			return f, false
		}
	}
	return f, true
}

func extFromText(s string) Ext {
	for e, t := range extText {
		if t != "" && t == s {
			return Ext(e)
		}
	}
	return 0
}

// ordinal reads an all-digit segment from 1 to maxOrdinal; a leading zero is
// refused so two names never carry the same ordinal.
func ordinal(s string) (int, bool) {
	if s == "" || s[0] == '0' {
		return 0, false
	}
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(s)
	return n, err == nil && n <= maxOrdinal
}
