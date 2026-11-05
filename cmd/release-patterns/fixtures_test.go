package main

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestExtractFixture(t *testing.T) {
	const src = `public class F
{
    [TestCase("Movie (2020) [Group]", "Group")]
    [TestCase(@"C:\Movies\Movie ""Cut"".mkv",
              null)]
    [TestCase("Movie \u00EB", -1)]
    [Test]
    public void should_parse_group(string title, string expected)
    {
        Parser.ParseReleaseGroup(title).Should().Be(expected);
    }

    [Test]
    public void no_cases()
    {
        Parser.Other();
    }
}`
	methods, err := extractFixture([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(methods) != 1 || methods[0].Name != "should_parse_group" {
		t.Fatalf("methods = %+v, want should_parse_group alone", methods)
	}
	m := methods[0]
	if want := []string{"Parser.ParseReleaseGroup", "title", ".Should", ".Be", "expected"}; !slices.Equal(m.Body, want) {
		t.Errorf("body = %q, want %q", m.Body, want)
	}
	var got []string
	for _, c := range m.Cases {
		b, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, string(b))
	}
	want := []string{
		`["Movie (2020) [Group]","Group"]`,
		`["C:\\Movies\\Movie \"Cut\".mkv",null]`,
		`["Movie ë",{"code":"-1"}]`,
	}
	if !slices.Equal(got, want) {
		t.Errorf("cases = %q, want %q", got, want)
	}
}

func TestExtractFixtureRefuses(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"rows with no method", `[TestCase("a")] }`, "no method after them"},
		{"concatenated argument", `[TestCase("a" + "b")] public void m() {}`, "is not one literal"},
		{"unterminated attribute", `[TestCase("a"`, "unterminated [TestCase]"},
		{"unterminated body", `[TestCase("a")] public void m() { x`, "unterminated method body"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := extractFixture([]byte(c.src))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want one naming %q", err, c.want)
			}
		})
	}
}
