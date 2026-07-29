package subsync

// allPostProcess returns PostProcessOptions with every step enabled. Used
// across test files that need a fully-configured post-processing pass.
func allPostProcess() PostProcessOptions {
	return PostProcessOptions{
		StripHI:              true,
		StripTags:            true,
		NormalizeEncoding:    true,
		NormalizeLineEndings: true,
		CleanWhitespace:      true,
		RemoveEmpty:          true,
	}
}
