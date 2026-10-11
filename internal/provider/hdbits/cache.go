package hdbits

// ClearCache removes all cached download data. Called after scan completion
// to free memory.
func (p *source) ClearCache() {
	p.dlCache.clear()
	p.torrentCache.Clear()
}
