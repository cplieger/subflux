package language

// FormatVersion changes with any tag's canonical text: a langtag or x/text
// bump that respells one, a fold rule, a language entering or leaving
// MultiScript. Storage folds it into every tag-holding codec version, so a
// bump resets core state; testdata/format-v<N>.golden pins version N.
const FormatVersion uint16 = 1
