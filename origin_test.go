package truegrain

import (
	"context"
	"net/http"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// Where the served model came from.
//
// This field was missing for a while and nothing caught it: the operation
// coverage test checks that every operationId has a method, not that every
// documented field has somewhere to land. Reload's own documentation told a
// caller to read Origin.Commit, and a caller who tried could not compile.
//
// So these test the field against the vendored spec rather than against what
// this file happens to believe.

// originPropertyPattern reads the property names under Health's `origin`.
//
// A regular expression rather than a YAML parser, for the reason the rest of
// this module gives: a caller asking for a number should not inherit one.
// The block is found by its key and bounded by the next line at the same
// indentation, and TestTheOriginScanFindsSomething guards that.
var originPropertyPattern = regexp.MustCompile(`(?m)^ {12}(\w+):\s*$`)

func specOriginProperties(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile("spec/openapi.yaml")
	if err != nil {
		t.Fatalf("the vendored spec is missing: %v", err)
	}
	// Normalised, because a Windows checkout has CRLF and a Linux one does
	// not. A scan that depended on which would pass on one machine and fail
	// on the other, or worse, match nothing and pass on both.
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")

	start := strings.Index(text, "\n        origin:\n")
	if start < 0 {
		t.Fatal("the spec no longer describes health.origin; if the engine dropped it, " +
			"drop Health.Origin too rather than leaving a field nothing fills")
	}
	rest := text[start+1:]
	props := strings.Index(rest, "\n          properties:\n")
	if props < 0 {
		t.Fatal("health.origin has no properties block")
	}
	rest = rest[props+1:]
	// The block ends at the next key one level out, which is the next sibling
	// of `origin` under Health's own properties.
	if end := regexp.MustCompile(`(?m)^ {8}\w+:`).FindStringIndex(rest); end != nil {
		rest = rest[:end[0]]
	}

	matches := originPropertyPattern.FindAllStringSubmatch(rest, -1)
	names := make([]string, 0, len(matches))
	for _, m := range matches {
		names = append(names, m[1])
	}
	return names
}

func TestTheOriginScanFindsSomething(t *testing.T) {
	// Guards the scan itself. A regular expression that silently matches
	// nothing would make the test below pass vacuously, which is worse than
	// not having it.
	if n := len(specOriginProperties(t)); n < 3 {
		t.Fatalf("the scan found %d origin properties, which cannot be right; "+
			"the spec's indentation has probably moved", n)
	}
}

// TestOriginCarriesEveryFieldTheSpecDocuments is the test that was missing.
func TestOriginCarriesEveryFieldTheSpecDocuments(t *testing.T) {
	carried := map[string]bool{}
	origin := reflect.TypeOf(Origin{})
	for i := range origin.NumField() {
		tag := origin.Field(i).Tag.Get("json")
		carried[strings.Split(tag, ",")[0]] = true
	}

	for _, name := range specOriginProperties(t) {
		if !carried[name] {
			t.Errorf("the engine reports origin.%s and Origin has no field for it", name)
		}
	}
}

// TestHealthCarriesOrigin.
//
// Named for the thing a pipeline does. Reload deliberately names no commit,
// so the only way to know a deploy landed is to read it here; a Health
// without Origin makes the documented procedure impossible to write.
func TestHealthCarriesOrigin(t *testing.T) {
	client, _ := stub(t, func(recorded) (int, any) {
		return http.StatusOK, map[string]any{
			"workspace": "retail",
			"origin": map[string]any{
				"repository":   "https://github.com/acme/models.git",
				"ref":          "main",
				"commit":       "23523843c4f5b92df4f8628a96a79bb5cbbfa9ee",
				"subdirectory": "models",
			},
		}
	})

	health, err := client.Health(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if health.Origin == nil {
		t.Fatal("an engine following a repository reported no origin")
	}
	if health.Origin.Commit != "23523843c4f5b92df4f8628a96a79bb5cbbfa9ee" {
		t.Errorf("the commit was lost, so a deploy cannot be confirmed: %+v", health.Origin)
	}
	if health.Origin.Ref != "main" || health.Origin.Subdirectory != "models" {
		t.Errorf("origin was parsed incompletely: %+v", health.Origin)
	}
	// The URL must arrive as given. A credential in it is refused at startup,
	// so anything that looked like redaction here would be hiding a bug.
	if strings.Contains(health.Origin.Repository, "@") {
		t.Errorf("the repository URL carries userinfo: %q", health.Origin.Repository)
	}
}

// TestAnEngineReadingFromAPathReportsNoOrigin, so "not under version control"
// is a nil check rather than a struct of empty strings that reads like a
// repository nobody named.
func TestAnEngineReadingFromAPathReportsNoOrigin(t *testing.T) {
	client, _ := stub(t, func(recorded) (int, any) {
		return http.StatusOK, map[string]any{"workspace": "retail"}
	})

	health, err := client.Health(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if health.Origin != nil {
		t.Errorf("a path-backed engine reported an origin: %+v", health.Origin)
	}
}
