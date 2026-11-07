package hdbits_test

import (
	"net/http"
	"os"
	"testing"

	"github.com/cplieger/subflux/internal/provider/hdbits"
	"github.com/cplieger/subflux/internal/provider/providertest"
)

func TestContract(t *testing.T) {
	refusal, err := os.ReadFile("testdata/auth-failed.json")
	if err != nil {
		t.Fatal(err)
	}
	providertest.Contract(t, providertest.Harness{
		Entry:    hdbits.NewEntry(),
		Values:   values,
		Query:    movie(t, "fr"),
		Recorded: newRecorded(t),
		Refused:  http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(refusal) }),
	})
}
