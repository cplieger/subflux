package arr

import "strings"

// RootFolders drops each folder's trailing slashes, which Sonarr and Radarr
// keep as the user typed them, so a folder meets the Set's path rule; any
// other unclean folder is left for the Set to drop.
func RootFolders(paths []string) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		if trimmed := strings.TrimRight(p, "/"); trimmed != "" {
			p = trimmed
		}
		out[i] = p
	}
	return out
}
