package hdbits

// ClearCache removes all cached download data. Called after scan completion
// to free memory.
func (p *Provider) ClearCache() {
	p.dlCache.Clear()
	p.torrentCache.Clear()
}
