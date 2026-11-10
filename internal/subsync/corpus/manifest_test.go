package corpus

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
)

// harnessFile is one file of the reference-sync harness set: its study
// name, its path in the archive, and its SHA-256.
type harnessFile struct {
	name, path, sha256 string
}

var (
	commitLine  = regexp.MustCompile(`^# commit\t([0-9a-f]{40})$`)
	studyName   = regexp.MustCompile(`^[a-z]+__g[0-2]__(0[1-9]|1[0-2])\.(ass|srt)$`)
	sha256Value = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// readHarnessManifest reads the harness manifest: the archive commit the set
// is pinned to, then one row per file, sorted by study name. It refuses a
// malformed or repeated row, so the set it names is exact.
func readHarnessManifest(path string) (commit string, files []harnessFile, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	if !sc.Scan() {
		return "", nil, fmt.Errorf("%s: empty", path)
	}
	m := commitLine.FindStringSubmatch(sc.Text())
	if m == nil {
		return "", nil, fmt.Errorf("%s: the first line is not the pinned commit", path)
	}
	if !sc.Scan() || sc.Text() != "name\tpath\tsha256" {
		return "", nil, fmt.Errorf("%s: missing the header line", path)
	}
	for line := 3; sc.Scan(); line++ {
		cols := strings.Split(sc.Text(), "\t")
		switch {
		case len(cols) != 3:
			return "", nil, fmt.Errorf("%s:%d: %d columns, want 3", path, line, len(cols))
		case !studyName.MatchString(cols[0]):
			return "", nil, fmt.Errorf("%s:%d: %q is not a study name", path, line, cols[0])
		case cols[1] == "" || strings.HasPrefix(cols[1], "/") || strings.Contains(cols[1], ".."):
			return "", nil, fmt.Errorf("%s:%d: archive path %q", path, line, cols[1])
		case !sha256Value.MatchString(cols[2]):
			return "", nil, fmt.Errorf("%s:%d: %q is not a SHA-256", path, line, cols[2])
		case len(files) > 0 && cols[0] <= files[len(files)-1].name:
			return "", nil, fmt.Errorf("%s:%d: %s out of order or repeated", path, line, cols[0])
		}
		files = append(files, harnessFile{name: cols[0], path: cols[1], sha256: cols[2]})
	}
	return m[1], files, sc.Err()
}

// devBoxRow is one dev-box case: an opaque id, the reference and candidate
// tracks' languages and formats, and the SHA-256 of its three files.
type devBoxRow struct {
	id, refLang, candLang, refExt, candExt string
	videoSHA, refSHA, candSHA              string
}

func (r *devBoxRow) video() string    { return r.id + ".mka" }
func (r *devBoxRow) refFile() string  { return r.id + ".ref." + r.refExt }
func (r *devBoxRow) candFile() string { return r.id + ".cand." + r.candExt }

const devBoxHeader = "id\tref_lang\tcand_lang\tref_ext\tcand_ext\tmka_sha256\tref_sha256\tcand_sha256"

var (
	devBoxID  = regexp.MustCompile(`^[0-9a-f]{12}$`)
	iso6392   = regexp.MustCompile(`^[a-z]{3}$`)
	trackExts = map[string]bool{"srt": true, "ass": true}
)

// readDevBoxManifest reads the dev-box manifest, refusing a malformed or
// repeated row, a reference and candidate in one language, or rows out of
// order.
func readDevBoxManifest(path string) ([]devBoxRow, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	if !sc.Scan() || sc.Text() != devBoxHeader {
		return nil, fmt.Errorf("%s: missing the header line", path)
	}
	var rows []devBoxRow
	for line := 2; sc.Scan(); line++ {
		c := strings.Split(sc.Text(), "\t")
		switch {
		case len(c) != 8:
			return nil, fmt.Errorf("%s:%d: %d columns, want 8", path, line, len(c))
		case !devBoxID.MatchString(c[0]):
			return nil, fmt.Errorf("%s:%d: id %q", path, line, c[0])
		case !iso6392.MatchString(c[1]) || !iso6392.MatchString(c[2]) || c[1] == c[2]:
			return nil, fmt.Errorf("%s:%d: languages %q and %q", path, line, c[1], c[2])
		case !trackExts[c[3]] || !trackExts[c[4]]:
			return nil, fmt.Errorf("%s:%d: formats %q and %q", path, line, c[3], c[4])
		case !sha256Value.MatchString(c[5]) || !sha256Value.MatchString(c[6]) || !sha256Value.MatchString(c[7]):
			return nil, fmt.Errorf("%s:%d: a column is not a SHA-256", path, line)
		case len(rows) > 0 && c[0] <= rows[len(rows)-1].id:
			return nil, fmt.Errorf("%s:%d: %s out of order or repeated", path, line, c[0])
		}
		rows = append(rows, devBoxRow{id: c[0], refLang: c[1], candLang: c[2], refExt: c[3], candExt: c[4], videoSHA: c[5], refSHA: c[6], candSHA: c[7]})
	}
	return rows, sc.Err()
}

func TestDevBoxManifestHoldsTheSet(t *testing.T) {
	t.Parallel()
	rows, err := readDevBoxManifest("manifest.tsv")
	if err != nil {
		t.Fatal(err)
	}
	// The set built from the dev box's library: 70 TV, 40 anime and 20
	// movie videos.
	if len(rows) != 130 {
		t.Errorf("manifest holds %d cases, want 130", len(rows))
	}
}

func TestReadDevBoxManifestRefuses(t *testing.T) {
	t.Parallel()
	h := strings.Repeat("0123456789abcdef", 4)
	row := "0123456789ab\teng\tfre\tsrt\tass\t" + h + "\t" + h + "\t" + h + "\n"
	for name, body := range map[string]string{
		"no header":       row,
		"a short row":     devBoxHeader + "\n0123456789ab\teng\tfre\n",
		"a bad id":        devBoxHeader + "\n" + strings.Replace(row, "0123456789ab", "Show S01E01", 1),
		"one language":    devBoxHeader + "\n" + strings.Replace(row, "fre", "eng", 1),
		"a bad format":    devBoxHeader + "\n" + strings.Replace(row, "\tass\t", "\tpgs\t", 1),
		"a bad hash":      devBoxHeader + "\n" + strings.Replace(row, "abcdef", "ABCDEF", 1),
		"a repeated case": devBoxHeader + "\n" + row + row,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			path := t.TempDir() + "/manifest.tsv"
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			if rows, err := readDevBoxManifest(path); err == nil {
				t.Errorf("readDevBoxManifest = %v, nil; want a refusal", rows)
			}
		})
	}
}

func TestHarnessManifestNamesTheStudySet(t *testing.T) {
	t.Parallel()
	_, files, err := readHarnessManifest("harness-manifest.tsv")
	if err != nil {
		t.Fatal(err)
	}
	// The study's inventory: 8 series, episodes 1 to 12 of groups 0 to 2,
	// 256 files (sasami's group 0 holds 4 episodes; eva and sg have no
	// group 2).
	groups := map[string]int{}
	for _, f := range files {
		groups[f.name[:strings.Index(f.name, "__g")+4]]++
	}
	want := map[string]int{
		"apollon__g0": 12, "apollon__g1": 12, "apollon__g2": 12, "bebop__g0": 12, "bebop__g1": 12, "bebop__g2": 12,
		"erased__g0": 12, "erased__g1": 12, "erased__g2": 12, "eva__g0": 12, "eva__g1": 12,
		"fz__g0": 12, "fz__g1": 12, "fz__g2": 12, "sasami__g0": 4, "sasami__g1": 12, "sasami__g2": 12,
		"sg__g0": 12, "sg__g1": 12, "snk__g0": 12, "snk__g1": 12, "snk__g2": 12,
	}
	if len(files) != 256 || len(groups) != len(want) {
		t.Fatalf("manifest holds %d files in %d groups, want 256 in %d", len(files), len(groups), len(want))
	}
	for g, n := range want {
		if groups[g] != n {
			t.Errorf("group %s holds %d files, want %d", g, groups[g], n)
		}
	}
}

func TestReadHarnessManifestRefuses(t *testing.T) {
	t.Parallel()
	const head = "# commit\t6503dc71b42e286716f4afb07a667e150d3f0004\nname\tpath\tsha256\n"
	row := "bebop__g0__01.ass\tCowboy Bebop/01.ass\t" + strings.Repeat("0123456789abcdef", 4) + "\n"
	for name, body := range map[string]string{
		"no commit":        "name\tpath\tsha256\n" + row,
		"no header":        "# commit\t6503dc71b42e286716f4afb07a667e150d3f0004\n" + row,
		"a short row":      head + "bebop__g0__01.ass\tCowboy Bebop/01.ass\n",
		"a bad name":       head + strings.Replace(row, "bebop__g0__01", "bebop__g3__01", 1),
		"an escaping path": head + strings.Replace(row, "Cowboy Bebop/01.ass", "../01.ass", 1),
		"a bad hash":       head + strings.Replace(row, "abcdef", "ABCDEF", 1),
		"a repeated name":  head + row + row,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			path := t.TempDir() + "/manifest.tsv"
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, files, err := readHarnessManifest(path); err == nil {
				t.Errorf("readHarnessManifest = %v, nil; want a refusal", files)
			}
		})
	}
}
