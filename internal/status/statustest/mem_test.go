package statustest

import (
	"testing"

	"github.com/cplieger/subflux/internal/status"
)

func TestMemPersisterContract(t *testing.T) {
	PersisterContract(t, func(*testing.T) status.Persister { return NewMemPersister() })
}
