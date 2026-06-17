package boltkv

import (
	"testing"
	"time"
)

type codecRec struct {
	Imported time.Time `json:"imported"`
	Title    string    `json:"title"`
	Tags     []string  `json:"tags"`
	Score    int       `json:"score"`
	Manual   bool      `json:"manual"`
}

func TestEncodeDecode_roundTrip(t *testing.T) {
	want := codecRec{
		Title:    "Some Movie",
		Tags:     []string{"a", "b"},
		Imported: time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC),
		Score:    88,
		Manual:   true,
	}
	data, err := Encode(&want)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	var got codecRec
	if err := Decode(data, &got); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got.Title != want.Title || got.Score != want.Score || got.Manual != want.Manual {
		t.Errorf("scalar fields mismatch: got %+v want %+v", got, want)
	}
	if len(got.Tags) != 2 || got.Tags[0] != "a" || got.Tags[1] != "b" {
		t.Errorf("tags mismatch: got %v", got.Tags)
	}
	if !got.Imported.Equal(want.Imported) {
		t.Errorf("imported mismatch: got %v want %v", got.Imported, want.Imported)
	}
}

func TestDecode_malformedReturnsError(t *testing.T) {
	var got codecRec
	if err := Decode([]byte("not json"), &got); err == nil {
		t.Error("Decode on malformed bytes: err = nil, want error")
	}
}

func TestDecodeOrHandle_tolerantSkip(t *testing.T) {
	var got codecRec
	skip, err := DecodeOrHandle(TolerantSkip, "subtitle_state", []byte("k"), []byte("{bad"), &got)
	if err != nil {
		t.Errorf("tolerant-skip returned err = %v, want nil", err)
	}
	if !skip {
		t.Error("tolerant-skip returned skip = false, want true on malformed input")
	}
}

func TestDecodeOrHandle_failClosed(t *testing.T) {
	var got codecRec
	skip, err := DecodeOrHandle(FailClosed, "auth_users", []byte("k"), []byte("{bad"), &got)
	if err == nil {
		t.Error("fail-closed returned err = nil, want error on malformed input")
	}
	if skip {
		t.Error("fail-closed returned skip = true, want false")
	}
}

func TestDecodeOrHandle_validNoSkip(t *testing.T) {
	enc, err := Encode(&codecRec{Title: "ok"})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	var got codecRec
	for _, mode := range []DecodeMode{FailClosed, TolerantSkip} {
		skip, derr := DecodeOrHandle(mode, "b", []byte("k"), enc, &got)
		if derr != nil || skip {
			t.Errorf("mode %s on valid data: skip=%v err=%v, want skip=false err=nil", mode, skip, derr)
		}
	}
	if got.Title != "ok" {
		t.Errorf("decoded title = %q, want %q", got.Title, "ok")
	}
}
