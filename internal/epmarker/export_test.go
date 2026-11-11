package epmarker

// Find is every well-formed single marker in name, in order of appearance
// across both notations: the reading Claims, Present and FirstIndex share.
func Find(name string) []Marker {
	r := read(name)
	return r.markers()
}
