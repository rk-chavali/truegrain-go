package truegrain

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

// The record an operator reads after an agent gave up.
//
// Three things can go wrong here and each is its own test: the events come
// back wrong, a filter the engine would reject is sent anyway, or a 404 from
// an engine with no reader configured is reported as a transport failure
// instead of as the refusal it is.

func TestAuditReadsTheDecisionsBack(t *testing.T) {
	client, seen := stub(t, func(recorded) (int, any) {
		return http.StatusOK, map[string]any{
			"count": 1,
			"events": []map[string]any{{
				"time":         "2026-09-19T10:15:00Z",
				"identity":     "agent@acme.com",
				"decision":     "refused",
				"refusal_code": "fan_out_would_inflate",
				"retry":        "modify",
				"hint":         "ask for order_count on its own",
			}},
		}
	})

	events, err := client.Audit(context.Background(), DecisionRefused, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("want one event, got %d", len(events))
	}
	// The refusal code is the field a caller branches on, so losing it while
	// keeping the message would leave the record readable but not usable.
	if events[0].RefusalCode != "fan_out_would_inflate" || events[0].Retry != RetryModify {
		t.Errorf("the refusal was not parsed: %+v", events[0])
	}
	if events[0].Time.IsZero() {
		t.Error("the timestamp was dropped, which unorders the window")
	}

	q := (*seen)[0].Query
	if !strings.Contains(q, "decision=refused") || !strings.Contains(q, "limit=50") {
		t.Errorf("the filter was not sent: %q", q)
	}
}

// TestAuditRefusesAnUnknownDecisionWithoutAsking. The engine answers 400 for a
// decision outside its enum, and a round trip to learn that is a worse error
// message than naming the legal values here.
func TestAuditRefusesAnUnknownDecisionWithoutAsking(t *testing.T) {
	client, seen := stub(t, func(recorded) (int, any) {
		return http.StatusOK, map[string]any{"events": []any{}, "count": 0}
	})

	if _, err := client.Audit(context.Background(), "rejected", 0); err == nil {
		t.Fatal("an unknown decision was sent to the engine")
	}
	if len(*seen) != 0 {
		t.Errorf("a request was made anyway: %+v", *seen)
	}
}

// TestAuditReportsAnAbsentRecordAsARefusal. An engine with no reader named
// answers 404 and one where the caller is not a named reader answers 403.
// Both are answers about access, so both must arrive as *Refused rather than
// as a transport error a caller cannot branch on.
func TestAuditReportsAnAbsentRecordAsARefusal(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusForbidden} {
		client, _ := stub(t, func(recorded) (int, any) {
			return status, map[string]any{
				"code":   "audit_not_readable",
				"reason": "this engine names no reader for the record",
				"retry":  "never",
			}
		})

		_, err := client.Audit(context.Background(), "", 0)
		var refused *Refused
		if !errors.As(err, &refused) {
			t.Fatalf("HTTP %d did not arrive as a refusal: %v", status, err)
		}
		if refused.Status != status || !refused.IsFinal() {
			t.Errorf("HTTP %d: %+v", status, refused)
		}
	}
}

// TestAuditClampsTheLimitTheEngineWouldReject, rather than letting a caller
// who asked for everything get a 400 and no record at all.
func TestAuditClampsTheLimitTheEngineWouldReject(t *testing.T) {
	client, seen := stub(t, func(recorded) (int, any) {
		return http.StatusOK, map[string]any{"events": []any{}, "count": 0}
	})

	if _, err := client.Audit(context.Background(), "", 100000); err != nil {
		t.Fatal(err)
	}
	if q := (*seen)[0].Query; !strings.Contains(q, "limit=200") {
		t.Errorf("the limit was not clamped: %q", q)
	}
}
