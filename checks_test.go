package truegrain

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

// The metadata surface.
//
// These parse rather than compute, so the tests are about the two ways that
// goes wrong: a field quietly dropped on the way in, and a 404 meaning "not
// configured" arriving as something a caller cannot branch on.

func TestDoctorKeepsTheVerdictSeparateFromTheFindings(t *testing.T) {
	client, _ := stub(t, func(recorded) (int, any) {
		return http.StatusOK, map[string]any{
			"ok":             false,
			"tables_checked": 12,
			"findings": []map[string]any{{
				"severity": "error",
				"dataset":  "orders",
				"field":    "region",
				"source":   "analytics.fct_orders",
				"message":  "the model names a column the warehouse does not have",
				"hint":     "drop the field, or point it at the column that replaced it",
			}, {
				"severity": "warning",
				"dataset":  "customers",
				"message":  "the warehouse type widened",
			}},
		}
	})

	got, err := client.Doctor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.OK || got.TablesChecked != 12 || len(got.Findings) != 2 {
		t.Fatalf("the report was not parsed: %+v", got)
	}
	// Severity is the whole point: a warning and an error read the same to
	// anybody who only counts findings.
	if got.Findings[0].Severity != SeverityError || got.Findings[1].Severity != SeverityWarning {
		t.Errorf("the severities were lost: %+v", got.Findings)
	}
	if got.Findings[0].Hint == "" || got.Findings[0].Source == "" {
		t.Errorf("the actionable half was dropped: %+v", got.Findings[0])
	}
}

// TestDoctorReportsWhyNothingWasChecked.
//
// An executor that cannot introspect produces no findings, which is exactly
// what a healthy model produces. Without Skipped the two are identical, and
// the wrong one is reassuring.
func TestDoctorReportsWhyNothingWasChecked(t *testing.T) {
	client, _ := stub(t, func(recorded) (int, any) {
		return http.StatusOK, map[string]any{
			"ok": true, "tables_checked": 0, "findings": []any{},
			"skipped": "this executor cannot describe the warehouse",
		}
	})

	got, err := client.Doctor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Skipped == "" {
		t.Error("a check that never ran is indistinguishable from a clean one")
	}
}

// TestNotConfiguredArrivesAsARefusal.
//
// Four endpoints answer 404 for "nobody set this up", and all four would
// otherwise be reported as a transport failure a caller cannot branch on.
func TestNotConfiguredArrivesAsARefusal(t *testing.T) {
	for _, c := range []struct {
		name string
		call func(*Client) error
	}{
		{"no test suite", func(cl *Client) error { _, err := cl.RunTests(context.Background()); return err }},
		{"never reloaded", func(cl *Client) error { _, err := cl.Diff(context.Background()); return err }},
		{"nothing scheduled", func(cl *Client) error { _, err := cl.DoctorHistory(context.Background()); return err }},
		{"not following git", func(cl *Client) error { _, err := cl.Reload(context.Background()); return err }},
	} {
		t.Run(c.name, func(t *testing.T) {
			client, _ := stub(t, func(recorded) (int, any) {
				return http.StatusNotFound, map[string]any{
					"code": "not_served", "reason": "nobody configured this", "retry": "never",
				}
			})
			var refused *Refused
			if err := c.call(client); !errors.As(err, &refused) {
				t.Fatalf("not reported as a refusal: %v", err)
			}
			if !refused.IsFinal() {
				t.Errorf("a caller would retry something nobody configured: %+v", refused)
			}
		})
	}
}

// TestRunTestsReportsWhatItDidNotRun.
//
// The failure this guards is a green suite that never executed half its
// cases, which is worse than a red one.
func TestRunTestsReportsWhatItDidNotRun(t *testing.T) {
	client, seen := stub(t, func(recorded) (int, any) {
		return http.StatusOK, map[string]any{
			"ok": true, "passed": 3, "failed": 0, "skipped": 0, "withheld": 4,
			"results": []map[string]any{
				{"name": "a fan-out is refused", "passed": true, "duration_ms": 12},
				{"name": "revenue by region", "passed": false, "skipped": true,
					"reason": "this credential may not run a query"},
			},
		}
	})

	got, err := client.RunTests(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !got.OK {
		t.Fatal("the suite reported ok and the client disagreed")
	}
	if got.Withheld != 4 {
		t.Errorf("withheld was dropped, so ok reads as a full pass: %+v", got)
	}
	if got.Results[1].Reason == "" || got.Results[0].DurationMS != 12 {
		t.Errorf("the case detail was lost: %+v", got.Results)
	}
	if (*seen)[0].Method != http.MethodPost {
		t.Errorf("tests were requested with %s", (*seen)[0].Method)
	}
}

func TestPolicyCarriesTheNotesThatSayWhatIsNotEnforced(t *testing.T) {
	client, _ := stub(t, func(recorded) (int, any) {
		return http.StatusOK, map[string]any{
			"governance": map[string]any{
				"resolver": "allow-all", "column_level": false,
				"note": "every caller may read every column",
			},
			"enforcement_notes": []string{
				"no column-level access control is configured",
			},
		}
	})

	got, err := client.Policy(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// An engine running allow-all has to say so, or a reader assumes a gate
	// exists because the product has one.
	if got.Governance.ColumnLevel {
		t.Error("allow-all was reported as making column-level decisions")
	}
	if len(got.EnforcementNotes) == 0 {
		t.Error("the notes stating the gaps were dropped, which is the one field nobody may lose")
	}
}

func TestExplainPolicySendsTheMetricAndRefusesAnEmptyOne(t *testing.T) {
	client, seen := stub(t, func(recorded) (int, any) {
		return http.StatusOK, map[string]any{
			"metric":   "retail.order_revenue",
			"identity": "analyst@acme.com",
			"readable": []string{"customers.region", "orders.placed_at"},
		}
	})

	got, err := client.ExplainPolicy(context.Background(), "retail.order_revenue")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Readable) != 2 || got.Identity != "analyst@acme.com" {
		t.Errorf("the explanation was not parsed: %+v", got)
	}
	if (*seen)[0].Body["metric"] != "retail.order_revenue" {
		t.Errorf("the metric was not sent: %+v", (*seen)[0].Body)
	}

	if _, err := client.ExplainPolicy(context.Background(), ""); err == nil {
		t.Error("an empty metric was sent to the engine")
	}
	if len(*seen) != 1 {
		t.Errorf("the empty metric made a request: %+v", *seen)
	}
}

// TestDiffCarriesTheBeforeAndAfter. Added and removed name things; altered is
// the one that says a number moved, and it is useless without both sides.
func TestDiffCarriesTheBeforeAndAfter(t *testing.T) {
	client, _ := stub(t, func(recorded) (int, any) {
		return http.StatusOK, map[string]any{
			"changed": true,
			"from":    "sha256:aaa", "to": "sha256:bbb",
			"added":   []string{"retail.refund_total"},
			"removed": []string{},
			"altered": map[string]any{
				"order_revenue by region": map[string]any{
					"before": "SELECT sum(amount) ...",
					"after":  "SELECT sum(amount_net) ...",
				},
			},
		}
	})

	got, err := client.Diff(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !got.Changed || got.From == got.To {
		t.Fatalf("the diff was not parsed: %+v", got)
	}
	change, ok := got.Altered["order_revenue by region"]
	if !ok {
		t.Fatalf("the altered request was lost: %+v", got.Altered)
	}
	if change.Before == "" || change.After == "" || change.Before == change.After {
		t.Errorf("a diff with one side is not a diff: %+v", change)
	}
}

// TestReloadSaysWhichOfTheTwoHappened, because a pipeline that treats
// "already running" as a failure retries a sync that is already under way.
func TestReloadSaysWhichOfTheTwoHappened(t *testing.T) {
	for _, want := range []string{ReloadReading, ReloadAlreadyRunning} {
		client, seen := stub(t, func(recorded) (int, any) {
			return http.StatusAccepted, map[string]any{"status": want}
		})

		got, err := client.Reload(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("status %q became %q", want, got)
		}
		// It means "look now", not "install this". A body would be a second
		// way into production.
		if (*seen)[0].Body != nil {
			t.Errorf("reload carried a payload: %+v", (*seen)[0].Body)
		}
	}
}

// TestReloadReportsAFailedLoadAsARefusal. The engine keeps serving the
// previous model and answers 502; a pipeline must be able to tell that from
// the network being down, because only one of them means the deploy did not
// land.
func TestReloadReportsAFailedLoadAsARefusal(t *testing.T) {
	client, _ := stub(t, func(recorded) (int, any) {
		return http.StatusBadGateway, map[string]any{
			"code":   "model_did_not_load",
			"reason": "the previously loaded model is still being served",
			"retry":  "modify",
		}
	})

	_, err := client.Reload(context.Background())
	var refused *Refused
	if !errors.As(err, &refused) {
		t.Fatalf("a failed load was not reported as a refusal: %v", err)
	}
	if !refused.ShouldModify() {
		t.Errorf("a broken model was reported as worth retrying unchanged: %+v", refused)
	}
}
