package subtitlefile

// LooksLikeText reports whether at most a tenth of head is control bytes
// other than TAB, LF, VT, FF, CR and ESC: subtitle text passes, a binary
// container does not.
func LooksLikeText(head []byte) bool {
	var n int
	for _, b := range head {
		if b < 0x09 || (b > 0x0D && b < 0x20 && b != 0x1B) {
			n++
		}
	}
	return n*10 <= len(head)
}
