package subsync

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"pgregory.net/rapid"
)

// --- StripHI ---

func TestStripHI_brackets(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, input, want string
	}{
		{"simple", "[gunshot] He fell.", "He fell."},
		{"multiple", "[door closes] Hello [music]", "Hello"},
		{"nested", "[[weird]] text", "] text"},
		{"empty result", "[all annotation]", ""},
		{"no brackets", "Normal dialogue", "Normal dialogue"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := strings.TrimSpace(stripHI(tt.input))
			if got != tt.want {
				t.Fatalf("stripHI(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestStripHI_parens(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, input, want string
	}{
		{"simple", "(laughing) That's funny.", "That's funny."},
		{"multiple", "(sighs) I know. (door closes)", "I know."},
		{"music", "(music playing) Do re mi", "Do re mi"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := strings.TrimSpace(stripHI(tt.input))
			if got != tt.want {
				t.Fatalf("stripHI(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestStripHI_music(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, input, want string
	}{
		{"music notes", "♪ Do re mi ♪", ""},
		{"double notes", "♫ Song lyrics ♫", ""},
		{"mixed", "Hello ♪ music ♪ world", "Hello  world"},
		{"standalone notes", "♪♪♪", ""},
		{"note with text", "♪", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := strings.TrimSpace(stripHI(tt.input))
			if got != tt.want {
				t.Fatalf("stripHI(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestStripHI_speaker_labels(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, input, want string
	}{
		{"simple", "JOHN: Hello there.", "Hello there."},
		{"with number", "MAN 1: Watch out!", "Watch out!"},
		{"with title", "DR. SMITH: The results are in.", "The results are in."},
		{"voiceover", "NARRATOR (V.O.): Long ago...", "Long ago..."},
		{"not uppercase", "john: hello", "john: hello"},
		{"no colon", "JOHN hello", "JOHN hello"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := strings.TrimSpace(stripHI(tt.input))
			if got != tt.want {
				t.Fatalf("stripHI(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestStripHI_multiline(t *testing.T) {
	t.Parallel()
	input := "[gunshot]\nJOHN: Run!\n♪ dramatic music ♪"
	got := strings.TrimSpace(stripHI(input))
	if got != "Run!" {
		t.Fatalf("got %q, want %q", got, "Run!")
	}
}

// --- StripTags ---

func TestStripTags(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, input, want string
	}{
		{"italic", "<i>Hello</i>", "Hello"},
		{"bold", "<b>World</b>", "World"},
		{"underline", "<u>Test</u>", "Test"},
		{"font", `<font color="#ffffff">Text</font>`, "Text"},
		{"nested", "<i><b>Bold italic</b></i>", "Bold italic"},
		{"no tags", "Plain text", "Plain text"},
		{"self-closing-like", "<i>Start</i> middle <b>end</b>", "Start middle end"},
		{"unknown tag preserved", "<span>Hello</span>", "<span>Hello</span>"},
		{"anchor preserved", `<a href="x">link</a>`, `<a href="x">link</a>`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := stripTags(tt.input)
			if got != tt.want {
				t.Fatalf("stripTags(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// --- CleanWhitespace ---

func TestCleanWhitespace(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, input, want string
	}{
		{"trim spaces", "  hello  ", "hello"},
		{"remove empty lines", "hello\n\nworld", "hello\nworld"},
		{"remove dash only", "- \nhello", "hello"},
		{"preserve content", "hello\nworld", "hello\nworld"},
		{"all empty", "\n\n\n", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := cleanWhitespace(tt.input)
			if got != tt.want {
				t.Fatalf("cleanWhitespace(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// --- RemoveEmpty ---

func TestRemoveEmpty(t *testing.T) {
	t.Parallel()
	cues := []Cue{
		{Start: 0, End: time.Second, Text: "Hello"},
		{Start: time.Second, End: 2 * time.Second, Text: ""},
		{Start: 2 * time.Second, End: 3 * time.Second, Text: "  "},
		{Start: 3 * time.Second, End: 4 * time.Second, Text: "World"},
	}
	got := removeEmpty(cues)
	if len(got) != 2 {
		t.Fatalf("expected 2 cues, got %d", len(got))
	}
	if got[0].Text != "Hello" || got[1].Text != "World" {
		t.Fatalf("unexpected cues: %v", got)
	}
}

// --- PostProcess integration ---

func TestPostProcess_full_pipeline(t *testing.T) {
	t.Parallel()
	cues := []Cue{
		{Start: 0, End: time.Second, Text: "<i>[gunshot]</i>"},
		{Start: time.Second, End: 2 * time.Second, Text: "JOHN: <b>Run!</b>"},
		{Start: 2 * time.Second, End: 3 * time.Second, Text: "♪ dramatic music ♪"},
		{Start: 3 * time.Second, End: 4 * time.Second, Text: "  Normal dialogue  "},
	}

	got := PostProcess(cues, allPostProcess())

	// Cue 0: empty after stripping HI + tags.
	// Cue 1: "Run!" after stripping speaker + tags.
	// Cue 2: empty after stripping music.
	// Cue 3: "Normal dialogue" after trimming.
	if len(got) != 2 {
		t.Fatalf("expected 2 cues, got %d: %v", len(got), got)
	}
	if got[0].Text != "Run!" {
		t.Fatalf("cue 0: expected 'Run!', got %q", got[0].Text)
	}
	if got[1].Text != "Normal dialogue" {
		t.Fatalf("cue 1: expected 'Normal dialogue', got %q", got[1].Text)
	}
}

func TestPostProcess_no_options(t *testing.T) {
	t.Parallel()
	cues := []Cue{
		{Start: 0, End: time.Second, Text: "<i>[gunshot]</i>"},
	}
	got := PostProcess(cues, PostProcessOptions{})
	if len(got) != 1 {
		t.Fatalf("expected 1 cue with no processing, got %d", len(got))
	}
	if got[0].Text != "<i>[gunshot]</i>" {
		t.Fatalf("expected unchanged text, got %q", got[0].Text)
	}
}

func TestPostProcess_empty_cues(t *testing.T) {
	t.Parallel()
	got := PostProcess(nil, allPostProcess())
	if got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}

func TestPostProcess_strip_hi_only(t *testing.T) {
	t.Parallel()
	cues := []Cue{
		{Start: 0, End: time.Second, Text: "JOHN: [sighs] Hello"},
	}
	got := PostProcess(cues, PostProcessOptions{StripHI: true, RemoveEmpty: true})
	if len(got) != 1 {
		t.Fatalf("expected 1 cue, got %d", len(got))
	}
	if got[0].Text != "Hello" {
		t.Fatalf("expected 'Hello', got %q", got[0].Text)
	}
}

func TestPostProcess_strip_tags_only(t *testing.T) {
	t.Parallel()
	cues := []Cue{
		{Start: 0, End: time.Second, Text: "<i>JOHN: [sighs] Hello</i>"},
	}
	got := PostProcess(cues, PostProcessOptions{StripTags: true})
	if len(got) != 1 {
		t.Fatalf("expected 1 cue, got %d", len(got))
	}
	// HI should be preserved, only tags stripped.
	if got[0].Text != "JOHN: [sighs] Hello" {
		t.Fatalf("expected 'JOHN: [sighs] Hello', got %q", got[0].Text)
	}
}

// --- NormalizeLineEndings ---

func TestNormalizeLineEndings(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, input, want string
	}{
		{"lf to crlf", "a\nb\n", "a\r\nb\r\n"},
		{"cr to crlf", "a\rb\r", "a\r\nb\r\n"},
		{"mixed", "a\r\nb\nc\r", "a\r\nb\r\nc\r\n"},
		{"trailing whitespace", "a\nb\n  \n", "a\r\nb\r\n"},
		{"empty", "", ""},
		{"whitespace only", "   \n  \n", ""},
		{"no trailing newline", "hello", "hello\r\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := string(normalizeLineEndings([]byte(tt.input)))
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// --- PostProcessBytes ---

func TestPostProcessBytes_encoding(t *testing.T) {
	t.Parallel()
	// UTF-8 BOM + content.
	input := append([]byte{0xEF, 0xBB, 0xBF}, []byte("Hello\n")...)
	got := PostProcessBytes(input, PostProcessOptions{
		NormalizeEncoding:    true,
		NormalizeLineEndings: true,
	})
	want := "Hello\r\n"
	if string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestPostProcessBytes_no_options(t *testing.T) {
	t.Parallel()
	// With no options enabled, data passes through unchanged.
	input := []byte{0xEF, 0xBB, 0xBF, 'H', 'i', '\n'}
	got := PostProcessBytes(input, PostProcessOptions{})
	if !bytes.Equal(got, input) {
		t.Fatalf("PostProcessBytes(no opts) = %x, want passthrough %x", got, input)
	}
}

func TestRemoveEmpty_all_empty(t *testing.T) {
	t.Parallel()
	cues := []Cue{
		{Start: 0, End: time.Second, Text: ""},
		{Start: time.Second, End: 2 * time.Second, Text: "   "},
		{Start: 2 * time.Second, End: 3 * time.Second, Text: "\n"},
	}
	got := removeEmpty(cues)
	if len(got) != 0 {
		t.Fatalf("removeEmpty(all empty) returned %d cues, want 0", len(got))
	}
}

func TestRemoveEmpty_nil_input(t *testing.T) {
	t.Parallel()
	got := removeEmpty(nil)
	if got != nil {
		t.Fatalf("removeEmpty(nil) = %v, want nil", got)
	}
}

func TestCleanWhitespace_dialogue_dash_with_content(t *testing.T) {
	t.Parallel()
	// "- text" should be preserved (only bare "-" is removed).
	got := cleanWhitespace("- Hello\n- World")
	if got != "- Hello\n- World" {
		t.Fatalf("cleanWhitespace(dialogue dash with content) = %q, want %q", got, "- Hello\n- World")
	}
}

func TestStripHI_speaker_with_apostrophe(t *testing.T) {
	t.Parallel()
	// Speaker label with apostrophe: O'BRIEN: should match.
	got := strings.TrimSpace(stripHI("O'BRIEN: Watch out!"))
	if got != "Watch out!" {
		t.Fatalf("stripHI(O'BRIEN:) = %q, want %q", got, "Watch out!")
	}
}

func TestStripHI_speaker_with_hyphen(t *testing.T) {
	t.Parallel()
	// Speaker label with hyphen: MARY-JANE: should match.
	got := strings.TrimSpace(stripHI("MARY-JANE: Hello!"))
	if got != "Hello!" {
		t.Fatalf("stripHI(MARY-JANE:) = %q, want %q", got, "Hello!")
	}
}

// --- PBT ---

// PBT: PostProcess never increases cue count.
func TestPostProcess_never_increases_cues(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(0, 20).Draw(t, "n")
		cues := make([]Cue, n)
		for i := range cues {
			cues[i] = Cue{
				Start: time.Duration(i) * time.Second,
				End:   time.Duration(i+1) * time.Second,
				Text:  rapid.String().Draw(t, "text"),
			}
		}
		got := PostProcess(cues, allPostProcess())
		if len(got) > len(cues) {
			t.Fatalf("PostProcess increased cue count: %d -> %d", len(cues), len(got))
		}
	})
}

// PBT: stripTags never leaves angle brackets from known tags.
func TestStripTags_no_known_tags_remain(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		tags := []string{"<i>", "</i>", "<b>", "</b>", "<u>", "</u>", "</font>"}
		text := rapid.String().Draw(t, "text")
		// Inject a random known tag.
		tag := tags[rapid.IntRange(0, len(tags)-1).Draw(t, "tag")]
		text = tag + text + tag
		got := stripTags(text)
		for _, known := range tags {
			if strings.Contains(got, known) {
				t.Fatalf("stripTags left %q in output: %q", known, got)
			}
		}
	})
}

// --- Non-mutation ---

func TestPostProcess_does_not_mutate_input(t *testing.T) {
	t.Parallel()
	cues := []Cue{
		{Start: 0, End: time.Second, Text: "<i>[gunshot]</i>"},
		{Start: time.Second, End: 2 * time.Second, Text: "Hello"},
	}
	origText0 := cues[0].Text
	origText1 := cues[1].Text
	_ = PostProcess(cues, allPostProcess())
	if cues[0].Text != origText0 {
		t.Fatalf("PostProcess mutated input cue 0: %q -> %q", origText0, cues[0].Text)
	}
	if cues[1].Text != origText1 {
		t.Fatalf("PostProcess mutated input cue 1: %q -> %q", origText1, cues[1].Text)
	}
}

// --- Idempotency PBTs ---

func TestPostProcess_idempotent(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(0, 10).Draw(t, "n")
		cues := make([]Cue, n)
		for i := range cues {
			cues[i] = Cue{
				Start: time.Duration(i) * time.Second,
				End:   time.Duration(i+1) * time.Second,
				Text:  rapid.String().Draw(t, "text"),
			}
		}
		opts := allPostProcess()
		once := PostProcess(cues, opts)
		twice := PostProcess(once, opts)
		if len(once) != len(twice) {
			t.Fatalf("PostProcess not idempotent: %d cues -> %d cues", len(once), len(twice))
		}
		for i := range once {
			if once[i].Text != twice[i].Text {
				t.Fatalf("PostProcess not idempotent at cue %d: %q -> %q", i, once[i].Text, twice[i].Text)
			}
		}
	})
}

func TestCleanWhitespace_idempotent(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		text := rapid.String().Draw(t, "text")
		once := cleanWhitespace(text)
		twice := cleanWhitespace(once)
		if once != twice {
			t.Fatalf("cleanWhitespace not idempotent: %q -> %q -> %q", text, once, twice)
		}
	})
}

func TestStripHI_idempotent(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		text := rapid.String().Draw(t, "text")
		once := stripHI(text)
		twice := stripHI(once)
		if once != twice {
			t.Fatalf("stripHI not idempotent: %q -> %q -> %q", text, once, twice)
		}
	})
}

func TestNormalizeLineEndings_idempotent(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		data := rapid.SliceOf(rapid.Byte()).Draw(t, "data")
		once := normalizeLineEndings(data)
		twice := normalizeLineEndings(once)
		if !bytes.Equal(once, twice) {
			t.Fatalf("normalizeLineEndings not idempotent: %x -> %x -> %x", data, once, twice)
		}
	})
}
