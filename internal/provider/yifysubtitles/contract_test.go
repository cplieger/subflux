package yifysubtitles_test

import (
	"net/http"
	"testing"

	"github.com/cplieger/subflux/internal/provider/providertest"
	"github.com/cplieger/subflux/internal/provider/yifysubtitles"
)

func TestContract(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/robots.txt", file("testdata/robots.txt"))
	mux.HandleFunc("/movie-imdb/tt1375666", file("testdata/movie-imdb-tt1375666.html"))
	mux.HandleFunc("/subtitles/", file("testdata/subtitle-page.html"))
	mux.HandleFunc("/subtitle/", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("PK\x03\x04")) })
	providertest.Contract(t, providertest.Harness{
		Entry:    yifysubtitles.NewEntry(),
		Query:    movie(t, "en"),
		Recorded: mux,
	})
}
