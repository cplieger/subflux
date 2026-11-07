package episodematch

import (
	"math"
	"slices"
	"testing"
)

// The expected tokens and similarities are titlematch/tm.py's norm_tokens
// and det_sim answers.

func TestNormTokens(t *testing.T) {
	for _, c := range []struct {
		title string
		want  []string
	}{
		{"... et on recommence (2)", []string{"et", "on", "recommence", "part2"}},
		{"A la croisée des chemins - 2ème Partie", []string{"croisee", "des", "chemins", "2eme", "partie"}},
		{"A.T.M.O.S. (1)", []string{"t", "m", "o", "s", "part1"}},
		{"Gaz à tous les étages", []string{"gaz", "tous", "etages"}},
		{"The 30 percent Iron Chef", []string{"30", "percent", "iron", "chef"}},
		{"Anthology of Interest II", []string{"anthology", "of", "interest", "part2"}},
		{"Jupiter Jazz, Part 1", []string{"jupiter", "jazz", "part1"}},
		{"Fry & the Slurm Factory", []string{"fry", "and", "slurm", "factory"}},
	} {
		if got := normTokens(c.title); !slices.Equal(got, c.want) {
			t.Errorf("normTokens(%q) = %q; want %q", c.title, got, c.want)
		}
	}
}

func TestSimilarity(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want float64
	}{
		{"Inside the Episode: Season 7 Episode 2 (2)", "Inside the Episode: Season 7 Episode 2", 0.96875},
		{"Episode 21 : Une Magie pour nettoyer les vêtements", "Épisode 4 : La magie pour sentir bon", 0.8610414746543777},
		{"Anthology of Interest II (2)", "Anthology of Interest II", 0.96},
		{"Celui qui découvrait le placard secret", "Celui qui avait un sac", 0.8531428571428571},
		{"Free Family Portrait Studio", "Family", 0.96},
		{"El Juego Comienza", "The One with Ross' Tan", 0.4696778711484594},
		{"The One Where Rachel Quits", "The Unicorn and the Wasp", 0.4835963804713805},
		{"a.t.m.o.s. (2)", "A.T.M.O.S. (2)", 1},
		{"Jupiter Jazz (1)", "Jupiter Jazz (2)", 0},
		{"", "Pilot", 0},
	} {
		a, b := normalize(c.a), normalize(c.b)
		if got := similarity(&a, &b); math.Abs(got-c.want) > 1e-12 {
			t.Errorf("similarity(%q, %q) = %v; want %v", c.a, c.b, got, c.want)
		}
	}
}
