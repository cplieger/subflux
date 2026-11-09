package statedb

import (
	"sync"
	"testing"
)

func TestAuthSecretsCreatedOnce(t *testing.T) {
	path := tempPath(t)
	s := mustOpen(t, path, productionConfig())
	var (
		wg   sync.WaitGroup
		got  [8]AuthSecrets
		errs [8]error
	)
	for i := range got {
		wg.Go(func() { got[i], errs[i] = s.AuthSecrets(t.Context()) })
	}
	wg.Wait()
	for i := range got {
		if errs[i] != nil {
			t.Fatalf("call %d: %v", i, errs[i])
		}
		if got[i] != got[0] {
			t.Errorf("call %d returned other keys than call 0", i)
		}
	}
	if got[0].FailureKey == ([32]byte{}) || got[0].DeviceKey == ([32]byte{}) || got[0].FailureKey == got[0].DeviceKey {
		t.Error("want two distinct random keys")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := mustOpen(t, path, productionConfig()).AuthSecrets(t.Context())
	if err != nil || again != got[0] {
		t.Errorf("after a reopen: %v, keys equal %v; want the stored keys", err, again == got[0])
	}
}
