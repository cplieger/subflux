package episodematch

import (
	"math"
	"slices"
	"strconv"
	"strings"
)

// A unit's pair is confident at a similarity of at least confidence with a
// lead of at least margin over its next-best episode: the thresholds the
// corpus measured (427 of 432 held-out members, no wrong episode), which
// TestAssignmentCorpus holds; at margin 0 one wrong episode is assigned.
const (
	confidence = 0.93
	margin     = 0.01
)

// unassigned marks a name whose title assigns it no episode.
const unassigned = -1

// assignTitles returns, per name, the index in titles (one entry per
// candidate episode, each holding that episode's titles) its title unit is
// confidently assigned to, else unassigned. Units, the members sharing a
// normalised title and a claim, are assigned all at once, one to one; two
// units each confidently best on one episode are both unassigned.
func assignTitles(names []Name, titles [][]string) []int {
	out := make([]int, len(names))
	for i := range out {
		out[i] = unassigned
	}
	unitOf, units := titleUnits(names)
	if len(units) == 0 || len(titles) == 0 {
		return out
	}
	score := scoreUnits(units, titles)
	best := make([]int, len(units))
	claimed := make(map[int]int, len(units))
	for u, row := range score {
		best[u] = unassigned
		if j := slices.Index(row, slices.Max(row)); row[j] >= confidence && row[j]-secondBest(row) >= margin {
			best[u] = j
			claimed[j]++
		}
	}
	col := hungarian(score)
	for i, u := range unitOf {
		if u >= 0 && best[u] >= 0 && claimed[best[u]] == 1 && col[u] == best[u] {
			out[i] = best[u]
		}
	}
	return out
}

// scoreUnits is each unit's best similarity to each episode's titles.
func scoreUnits(units []normTitle, titles [][]string) [][]float64 {
	cands := make([][]normTitle, len(titles))
	for j, ts := range titles {
		for _, t := range ts {
			if t != "" {
				cands[j] = append(cands[j], normalize(t))
			}
		}
	}
	score := make([][]float64, len(units))
	for u := range units {
		score[u] = make([]float64, len(cands))
		for j := range cands {
			for k := range cands[j] {
				score[u][j] = max(score[u][j], similarity(&units[u], &cands[j][k]))
			}
		}
	}
	return score
}

// titleUnits groups the titled names by normalised title and claims;
// unitOf[i] is name i's unit, -1 for an untitled name.
func titleUnits(names []Name) (unitOf []int, units []normTitle) {
	unitOf = make([]int, len(names))
	byKey := make(map[string]int)
	for i, n := range names {
		unitOf[i] = -1
		if n.Title == "" {
			continue
		}
		t := normalize(n.Title)
		var key strings.Builder
		key.WriteString(t.text)
		for _, p := range t.parts {
			key.WriteString("\x00" + p)
		}
		key.WriteString("\x01")
		for _, c := range n.Claims {
			key.WriteString(strconv.Itoa(c.Season) + "x" + strconv.Itoa(c.Episode) + ",")
		}
		u, ok := byKey[key.String()]
		if !ok {
			u = len(units)
			byKey[key.String()] = u
			units = append(units, t)
		}
		unitOf[i] = u
	}
	return unitOf, units
}

// secondBest is a row's second-largest score, 0 for a one-column row; a tie
// for the best gives the unit no margin.
func secondBest(row []float64) float64 {
	if len(row) < 2 {
		return 0
	}
	s := slices.Clone(row)
	slices.Sort(s)
	return s[len(s)-2]
}

// hungarian returns, per row of a score matrix, the column of a one-to-one
// assignment of maximum total score, or -1 for a row left without one when
// rows outnumber columns.
func hungarian(score [][]float64) []int {
	if len(score) > len(score[0]) {
		return hungarianTransposed(score)
	}
	n, m := len(score), len(score[0])
	h := potentials{
		score: score,
		u:     make([]float64, n+1), v: make([]float64, m+1), minv: make([]float64, m+1),
		p: make([]int, m+1), way: make([]int, m+1), used: make([]bool, m+1),
	}
	for i := 1; i <= n; i++ {
		h.augment(i)
	}
	out := make([]int, n)
	for j := 1; j <= m; j++ {
		if h.p[j] > 0 {
			out[h.p[j]-1] = j - 1
		}
	}
	return out
}

func hungarianTransposed(score [][]float64) []int {
	t := make([][]float64, len(score[0]))
	for j := range t {
		t[j] = make([]float64, len(score))
		for i := range score {
			t[j][i] = score[i][j]
		}
	}
	out := make([]int, len(score))
	for i := range out {
		out[i] = -1
	}
	for j, i := range hungarian(t) {
		out[i] = j
	}
	return out
}

// potentials is the shortest-augmenting-path form of the Hungarian algorithm
// over a cost of minus the score, rows and columns 1-based: p[j] is the row
// matched to column j, 0 for none.
type potentials struct {
	score      [][]float64
	u, v, minv []float64
	p, way     []int
	used       []bool
}

// augment matches row i, re-matching earlier rows along the cheapest path.
func (h *potentials) augment(i int) {
	h.p[0] = i
	j0 := 0
	for j := range h.minv {
		h.minv[j], h.used[j] = math.Inf(1), false
	}
	for {
		h.used[j0] = true
		j1, delta := h.relax(j0)
		h.shift(delta)
		j0 = j1
		if h.p[j0] == 0 {
			break
		}
	}
	for j0 != 0 {
		j1 := h.way[j0]
		h.p[j0] = h.p[j1]
		j0 = j1
	}
}

// relax lowers each free column's reduced cost through column j0's row and
// returns the free column of least reduced cost and that cost.
func (h *potentials) relax(j0 int) (col int, cost float64) {
	i0, delta, j1 := h.p[j0], math.Inf(1), 0
	for j := 1; j < len(h.minv); j++ {
		if h.used[j] {
			continue
		}
		if cur := -h.score[i0-1][j-1] - h.u[i0] - h.v[j]; cur < h.minv[j] {
			h.minv[j], h.way[j] = cur, j0
		}
		if h.minv[j] < delta {
			delta, j1 = h.minv[j], j
		}
	}
	return j1, delta
}

func (h *potentials) shift(delta float64) {
	for j := range h.minv {
		if h.used[j] {
			h.u[h.p[j]] += delta
			h.v[j] -= delta
		} else {
			h.minv[j] -= delta
		}
	}
}
