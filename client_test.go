package truegrain

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The client's job is transport, parsing and error mapping. Testing that
// against a real engine would test the engine instead, and would make these
// tests need a warehouse. A stub records what it was sent, which is how the
// request-shaping tests assert the client does not send fields the server
// would reject.

type recorded struct {
	Method string
	Path   string
	Query  string
	Body   map[string]any
	Auth   string
}

// stub serves canned responses and records every request.
func stub(t *testing.T, handler func(r recorded) (int, any)) (*Client, *[]recorded) {
	t.Helper()
	var seen []recorded

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := recorded{
			Method: r.Method,
			Path:   r.URL.Path,
			Query:  r.URL.RawQuery,
			Auth:   r.Header.Get("Authorization"),
		}
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&rec.Body)
		}
		seen = append(seen, rec)

		status, body := handler(rec)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(server.Close)

	client, err := New(server.URL, WithToken("test-token"))
	if err != nil {
		t.Fatal(err)
	}
	return client, &seen
}

var queryResponse = map[string]any{
	"columns":       []string{"region", "order_revenue"},
	"rows":          [][]any{{"NE", 750.0}, {"MW", 135.5}},
	"compiled_sql":  `SELECT "region", SUM("order_total") AS "order_revenue" FROM orders`,
	"model_version": "sha256:abc123",
	"dialect":       "duckdb",
	"namespace":     "sales",
}

func TestQueryParsesRowsAndProvenance(t *testing.T) {
	client, _ := stub(t, func(recorded) (int, any) { return 200, queryResponse })

	result, err := client.Query(context.Background(), Request{Metrics: []string{"sales.order_revenue"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.RowCount() != 2 {
		t.Errorf("want 2 rows, got %d", result.RowCount())
	}
	// Provenance travels with every result. Losing it at the client boundary
	// would defeat the point of carrying it.
	if result.ModelVersion != "sha256:abc123" {
		t.Errorf("the model version was dropped: %q", result.ModelVersion)
	}
	if !strings.Contains(result.CompiledSQL, "SUM") {
		t.Error("the compiled SQL was dropped")
	}
}

func TestMapsKeysRowsByColumn(t *testing.T) {
	client, _ := stub(t, func(recorded) (int, any) { return 200, queryResponse })
	result, err := client.Query(context.Background(), Request{Metrics: []string{"m"}})
	if err != nil {
		t.Fatal(err)
	}
	first := result.Maps()[0]
	if first["region"] != "NE" {
		t.Errorf("want region NE, got %v", first["region"])
	}
}

// TestAbsentFieldsAreOmitted: the server rejects unknown and malformed fields,
// and an empty list is not the same as absent.
func TestAbsentFieldsAreOmitted(t *testing.T) {
	client, seen := stub(t, func(recorded) (int, any) { return 200, queryResponse })

	if _, err := client.Query(context.Background(), Request{Metrics: []string{"m"}}); err != nil {
		t.Fatal(err)
	}
	body := (*seen)[0].Body
	for _, absent := range []string{"dimensions", "filters", "grain", "limit", "order_by"} {
		if _, present := body[absent]; present {
			t.Errorf("%q was sent although it was not set", absent)
		}
	}
}

// TestFiltersTravelAsStructuredValues is the injection guarantee at the client
// boundary: a value is data, never text spliced into a predicate.
func TestFiltersTravelAsStructuredValues(t *testing.T) {
	client, seen := stub(t, func(recorded) (int, any) { return 200, queryResponse })

	hostile := "'; DROP TABLE orders; --"
	_, err := client.Query(context.Background(), Request{
		Metrics: []string{"m"},
		Filters: []Filter{Eq("orders.status", hostile)},
	})
	if err != nil {
		t.Fatal(err)
	}

	filters, ok := (*seen)[0].Body["filters"].([]any)
	if !ok || len(filters) != 1 {
		t.Fatalf("the filter was not sent: %v", (*seen)[0].Body)
	}
	filter := filters[0].(map[string]any)
	if filter["op"] != "eq" || filter["dimension"] != "orders.status" {
		t.Errorf("the filter was reshaped: %v", filter)
	}
	if values := filter["values"].([]any); values[0] != hostile {
		t.Error("the value must be passed through unchanged, as data")
	}
}

func TestRefusalCarriesCodeAndRetryClass(t *testing.T) {
	client, _ := stub(t, func(recorded) (int, any) {
		return 422, map[string]any{
			"code":   "fan_out_would_inflate",
			"reason": "the total would be inflated",
			"hint":   "use line_revenue instead",
			"retry":  "modify",
		}
	})

	_, err := client.Query(context.Background(), Request{Metrics: []string{"m"}})

	var refusal *Refused
	if !errors.As(err, &refusal) {
		t.Fatalf("want a *Refused, got %T: %v", err, err)
	}
	if refusal.Code != "fan_out_would_inflate" {
		t.Errorf("want the code preserved, got %q", refusal.Code)
	}
	// The whole point: a caller branches on this rather than on the message.
	if !refusal.ShouldModify() || refusal.IsFinal() {
		t.Errorf("want retry modify, got %q", refusal.Retry)
	}
	if refusal.Status != 422 {
		t.Errorf("want the status preserved, got %d", refusal.Status)
	}
	if !strings.Contains(refusal.Error(), "line_revenue") {
		t.Error("the hint must survive into the error text; it names what to do instead")
	}
}

func TestDenialIsFinalSoACallerStops(t *testing.T) {
	client, _ := stub(t, func(recorded) (int, any) {
		return 403, map[string]any{"code": "access_denied", "reason": "no", "retry": "never"}
	})
	_, err := client.Query(context.Background(), Request{Metrics: []string{"m"}})

	var refusal *Refused
	if !errors.As(err, &refusal) {
		t.Fatalf("want a refusal, got %v", err)
	}
	if !refusal.IsFinal() || refusal.ShouldModify() {
		t.Error("a denial must classify as final so a caller stops rather than looping")
	}
}

func TestUnauthorizedIsNotARefusal(t *testing.T) {
	client, _ := stub(t, func(recorded) (int, any) {
		return 401, map[string]any{"code": "unauthenticated", "reason": "no"}
	})
	_, err := client.Health(context.Background())

	var unauth *UnauthorizedError
	if !errors.As(err, &unauth) {
		t.Fatalf("want an *UnauthorizedError, got %T: %v", err, err)
	}
}

func TestUnreachableEngineIsTransport(t *testing.T) {
	// A closed port is a deployment problem, never a statement about the
	// request, so it must not arrive as a refusal.
	client, err := New("http://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	client.HTTPClient = &http.Client{Timeout: time.Second}

	_, err = client.Health(context.Background())
	var transport *TransportError
	if !errors.As(err, &transport) {
		t.Fatalf("want a *TransportError, got %T: %v", err, err)
	}
}

func TestBearerTokenIsSent(t *testing.T) {
	client, seen := stub(t, func(recorded) (int, any) { return 200, map[string]any{} })
	if _, err := client.Health(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := (*seen)[0].Auth; got != "Bearer test-token" {
		t.Errorf("want the bearer token sent, got %q", got)
	}
}

func TestNoTokenSendsNoHeader(t *testing.T) {
	var seen string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("Authorization")
		_, _ = w.Write([]byte("{}"))
	}))
	defer server.Close()

	client, err := New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Health(context.Background()); err != nil {
		t.Fatal(err)
	}
	if seen != "" {
		t.Errorf("an unauthenticated client must send no Authorization header, sent %q", seen)
	}
}

func TestEmptyRequestIsRejectedBeforeItIsSent(t *testing.T) {
	client, seen := stub(t, func(recorded) (int, any) { return 200, queryResponse })

	if _, err := client.Query(context.Background(), Request{}); err == nil {
		t.Fatal("a request with no metrics must be rejected")
	}
	if len(*seen) != 0 {
		t.Error("a request the client knows is invalid must not reach the server")
	}
}

func TestMetricsNormalisesDimensionNames(t *testing.T) {
	// A listing returns dimension names and describeMetric returns objects. One
	// type serves both so a caller never has to know which produced the metric.
	client, _ := stub(t, func(r recorded) (int, any) {
		if strings.HasSuffix(r.Path, "/order_revenue") {
			return 200, map[string]any{
				"name":       "order_revenue",
				"dimensions": []map[string]any{{"name": "customers.region", "datatype": "String"}},
			}
		}
		return 200, map[string]any{
			"metrics": []map[string]any{{"name": "order_revenue", "dimensions": []string{"customers.region"}}},
		}
	})

	listed, err := client.Metrics(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || len(listed[0].Dimensions) != 1 || listed[0].Dimensions[0].Name != "customers.region" {
		t.Errorf("listing did not normalise dimensions: %+v", listed)
	}

	described, err := client.Metric(context.Background(), "order_revenue")
	if err != nil {
		t.Fatal(err)
	}
	if len(described.Dimensions) != 1 || described.Dimensions[0].Datatype != "String" {
		t.Errorf("describe lost dimension detail: %+v", described.Dimensions)
	}
}

func TestSearchReachesTheServer(t *testing.T) {
	client, seen := stub(t, func(recorded) (int, any) { return 200, map[string]any{"metrics": []any{}} })
	if _, err := client.Metrics(context.Background(), "revenue"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains((*seen)[0].Query, "search=revenue") {
		t.Errorf("the search term was not sent: %q", (*seen)[0].Query)
	}
}

// TestHealthParsesTheRealShape is a regression test.
//
// The first version of Health assumed `dialect` was a string. It is an object,
// reporting what the warehouse secures by itself, which is a different question
// from what the engine enforces. Stubbed tests written from the same wrong
// assumption passed; running against a real engine is what caught it.
func TestHealthParsesTheRealShape(t *testing.T) {
	client, _ := stub(t, func(recorded) (int, any) {
		return 200, map[string]any{
			"workspace":          "marketing, sales",
			"workspace_digest":   "sha256:a3d1609ce68b",
			"ossie_spec_version": "0.2.0.dev0",
			"dialect": map[string]any{
				"dialect":               "duckdb",
				"column_level_security": false,
				"row_level_security":    false,
				"note":                  "DuckDB has no column-level security.",
			},
			"governance": map[string]any{
				"resolver": "allow-all", "column_level": false, "note": "No access control.",
			},
			"executor":          "duckdb-cli",
			"metric_count":      10,
			"dimension_count":   9,
			"supported_grains":  []string{"day", "month"},
			"enforcement_notes": []string{"No column-level access control is configured."},
		}
	})

	health, err := client.Health(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// The warehouse's own security and the engine's are separate questions.
	if health.Dialect.Dialect != "duckdb" || health.Dialect.ColumnLevelSecurity {
		t.Errorf("the warehouse's own security was misparsed: %+v", health.Dialect)
	}
	if health.Governance.Resolver != "allow-all" || health.Governance.ColumnLevel {
		t.Errorf("the engine's governance was misparsed: %+v", health.Governance)
	}
	if health.WorkspaceDigest != "sha256:a3d1609ce68b" {
		t.Errorf("the digest was dropped: %q", health.WorkspaceDigest)
	}
	if health.MetricCount != 10 || health.DimensionCount != 9 {
		t.Errorf("counts were dropped: %d metrics, %d dimensions", health.MetricCount, health.DimensionCount)
	}
	// The gaps are what a reader needs most.
	if len(health.EnforcementNotes) != 1 {
		t.Errorf("the enforcement notes were dropped: %v", health.EnforcementNotes)
	}
}
