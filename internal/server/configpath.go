package server

import "github.com/cplieger/subflux/internal/config"

// cfgFilePath is the config file path inside the container.
// Tests override this via the package-level variable.
var cfgFilePath = config.DefaultConfigPath

// configFilePath returns the config file path.
func configFilePath() string {
	return cfgFilePath
}
