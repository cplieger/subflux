package settings

import (
	"os"
	"regexp"

	"github.com/cplieger/envx/yamlenv/v2"
	"github.com/cplieger/subflux/internal/formschema"
	"go.yaml.in/yaml/v3"
)

var subfluxName = regexp.MustCompile(`^SUBFLUX_[A-Z0-9_]+$`)

// lookupSubflux reads the environment for SUBFLUX_* names only, so a file
// cannot read any other variable of the process.
func lookupSubflux(name string) (string, bool) {
	if !subfluxName.MatchString(name) {
		return "", false
	}
	return os.LookupEnv(name)
}

func allowAll(string) bool { return true }

// expand expands every ${NAME} in the string scalar values under n, in place,
// one scalar at a time so each name left unexpanded is a field error at its
// scalar's path. A value a variable supplies is never scanned again, so it may
// hold ${.
func expand(n *yaml.Node, path string) []formschema.FieldError {
	switch n.Kind {
	case yaml.MappingNode:
		var errs []formschema.FieldError
		for i := 0; i+1 < len(n.Content); i += 2 {
			errs = append(errs, expand(n.Content[i+1], child(path, n.Content[i].Value))...)
		}
		return errs
	case yaml.SequenceNode:
		var errs []formschema.FieldError
		for i, e := range n.Content {
			errs = append(errs, expand(e, index(path, i))...)
		}
		return errs
	case yaml.ScalarNode:
		var errs []formschema.FieldError
		for _, name := range yamlenv.ExpandWith(n, allowAll, lookupSubflux) {
			errs = append(errs, formschema.FieldError{Path: path, Message: unresolvedMessage(name)})
		}
		return errs
	}
	return nil
}

func unresolvedMessage(name string) string {
	if subfluxName.MatchString(name) {
		return "${" + name + "} is not set in the environment"
	}
	return "${" + name + "} is not expanded: only ${SUBFLUX_...} variables are"
}
