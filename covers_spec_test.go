package truegrain

import (
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// The client is checked against the OpenAPI contract.
//
// It is hand-written rather than generated, because the value is in the parts
// no generator emits: the refusal semantics an agent branches on, and Run
// submitting a job and polling it so no caller writes that loop. The cost of
// hand-writing is that it can silently fall behind the engine. These tests are
// what pay it.
//
// The spec is vendored at spec/openapi.yaml and pinned to an engine commit in
// spec/PINNED_AT. Vendoring keeps this module buildable offline and makes a
// contract change a reviewable diff rather than a build that breaks one morning
// because something moved in another repository. A scheduled job compares the
// pin against the engine and opens a pull request when they diverge.

// operationIDs are read with a regular expression rather than a YAML parser.
//
// This module has no dependencies, and that is the whole promise of it: a
// caller asking for a number should not inherit a parser. An operationId line
// in the spec is `operationId: name` and nothing else, so scanning for it is
// exact rather than approximate, and the shape is guarded by
// TestSpecIsShapedTheWayThisScanAssumes below.
var operationIDPattern = regexp.MustCompile(`(?m)^\s*operationId:\s*(\S+)\s*$`)

func specOperationIDs(t *testing.T) []string {
	t.Helper()
	// Never skipped. The vendored spec is committed, so its absence is a broken
	// repository rather than a missing checkout, and a skipped contract test is
	// a contract test nobody notices is gone.
	raw, err := os.ReadFile("spec/openapi.yaml")
	if err != nil {
		t.Fatalf("the vendored spec is missing: %v", err)
	}
	matches := operationIDPattern.FindAllStringSubmatch(string(raw), -1)
	ids := make([]string, 0, len(matches))
	for _, m := range matches {
		ids = append(ids, m[1])
	}
	if len(ids) == 0 {
		t.Fatal("no operationIds found in the vendored spec")
	}
	return ids
}

// TestSpecIsShapedTheWayThisScanAssumes guards the regular expression above.
// If the spec ever wrote operationId inline or quoted, the scan would silently
// find nothing and every contract test would pass vacuously.
func TestSpecIsShapedTheWayThisScanAssumes(t *testing.T) {
	raw, err := os.ReadFile("spec/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)

	// Every occurrence of the key must be one the scan matches.
	total := strings.Count(text, "operationId")
	found := len(operationIDPattern.FindAllString(text, -1))
	if total != found {
		t.Errorf("the spec has %d operationId occurrences but the scan matched %d; "+
			"the contract tests would be checking an incomplete list", total, found)
	}
}

func TestClientCoversTheSpec(t *testing.T) {
	clientType := reflect.TypeOf(&Client{})

	for _, id := range specOperationIDs(t) {
		method, mapped := Operations[id]
		if !mapped {
			t.Errorf("the engine exposes %q but this client has no method for it; "+
				"add one and register it in Operations", id)
			continue
		}
		if _, ok := clientType.MethodByName(method); !ok {
			t.Errorf("Operations maps %q to Client.%s, which does not exist", id, method)
		}
	}
}

func TestClientClaimsNoOperationTheSpecDoesNotDefine(t *testing.T) {
	known := map[string]bool{}
	for _, id := range specOperationIDs(t) {
		known[id] = true
	}
	for id, method := range Operations {
		if !known[id] {
			t.Errorf("Operations names %q (Client.%s) which the spec does not define; "+
				"either the spec lost an endpoint or the mapping is wrong", id, method)
		}
	}
}

// TestNoMethodSendsSQL is the raw-SQL guarantee applied to this surface. There
// is no endpoint that accepts SQL, so there must be no method offering to send
// it, and no request field carrying it.
func TestNoMethodSendsSQL(t *testing.T) {
	clientType := reflect.TypeOf(&Client{})
	for i := range clientType.NumMethod() {
		name := strings.ToLower(clientType.Method(i).Name)
		if strings.Contains(name, "sql") || strings.Contains(name, "raw") {
			t.Errorf("Client.%s looks like a raw SQL escape hatch", clientType.Method(i).Name)
		}
	}

	requestType := reflect.TypeOf(Request{})
	for i := range requestType.NumField() {
		field := requestType.Field(i)
		if strings.Contains(strings.ToLower(field.Name), "sql") {
			t.Errorf("Request.%s would let a caller smuggle SQL through", field.Name)
		}
	}
}

// TestModuleHasNoDependencies is the promise this module makes. A caller asking
// for a number should not inherit a parser, an HTTP framework or a logger.
func TestModuleHasNoDependencies(t *testing.T) {
	raw, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "require") {
		t.Errorf("go.mod declares a dependency; this module must depend only on "+
			"the standard library:\n%s", raw)
	}
}
