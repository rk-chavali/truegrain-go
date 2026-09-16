package truegrain

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Asynchronous execution: submit, poll, page, cancel.
//
// These cover the client's side of the contract. The engine's side, including
// who may read a finished job, is tested in the engine repository.

const jobSQL = `SELECT "region", SUM("order_total") AS "order_revenue" FROM orders GROUP BY 1`

func accepted() map[string]any {
	return map[string]any{
		"job_id":        "j-1",
		"state":         "running",
		"compiled_sql":  jobSQL,
		"columns":       []string{"region", "order_revenue"},
		"model_version": "sha256:abc123",
		"namespace":     "sales",
		"dialect":       "duckdb",
	}
}

func finishedJob(rows [][]any, rowCount int, cursor string) map[string]any {
	if rowCount == 0 {
		rowCount = len(rows)
	}
	out := map[string]any{
		"job_id":        "j-1",
		"state":         "succeeded",
		"columns":       []string{"region", "order_revenue"},
		"rows":          rows,
		"row_count":     rowCount,
		"compiled_sql":  jobSQL,
		"model_version": "sha256:abc123",
	}
	if cursor != "" {
		out["next_cursor"] = cursor
	}
	return out
}

// jobServer serves a handler and records every request path.
func jobServer(t *testing.T, handler func(r *http.Request) (int, any)) (*Client, *[]string) {
	t.Helper()
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.RequestURI())
		status, body := handler(r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(server.Close)

	client, err := New(server.URL, WithToken("t"))
	if err != nil {
		t.Fatal(err)
	}
	return client, &calls
}

func fastPoll() RunOptions { return RunOptions{PollInterval: time.Millisecond} }

func TestSubmitReturnsSQLBeforeTheQueryFinishes(t *testing.T) {
	client, _ := jobServer(t, func(*http.Request) (int, any) { return 202, accepted() })

	job, err := client.Submit(context.Background(), Request{Metrics: []string{"m"}})
	if err != nil {
		t.Fatal(err)
	}
	if job.ID != "j-1" || job.State != StateRunning {
		t.Fatalf("unexpected job: %+v", job)
	}
	// Compilation is synchronous, so an agent can show its work without waiting
	// for the warehouse.
	if !strings.Contains(job.CompiledSQL, "SUM") {
		t.Error("a submitted job must carry the compiled SQL immediately")
	}
	if job.Done() {
		t.Error("a running job must not report as done")
	}
}

func TestRunPollsUntilDone(t *testing.T) {
	var polls atomic.Int32
	client, _ := jobServer(t, func(r *http.Request) (int, any) {
		if r.Method == http.MethodPost {
			return 202, accepted()
		}
		if polls.Add(1) < 3 {
			return 200, map[string]any{"job_id": "j-1", "state": "running"}
		}
		return 200, finishedJob([][]any{{"NE", 750.0}, {"MW", 135.5}}, 0, "")
	})

	result, err := client.Run(context.Background(), Request{Metrics: []string{"m"}}, fastPoll())
	if err != nil {
		t.Fatal(err)
	}
	if polls.Load() != 3 {
		t.Errorf("want 3 polls, got %d", polls.Load())
	}
	if result.RowCount() != 2 {
		t.Errorf("want 2 rows, got %d", result.RowCount())
	}
	// Provenance survives the asynchronous path exactly as it does the
	// synchronous one; that is the whole point of carrying it.
	if result.ModelVersion != "sha256:abc123" {
		t.Error("the model version was lost on the asynchronous path")
	}
}

func TestRunCollectsEveryPage(t *testing.T) {
	pages := map[string]map[string]any{
		"":   finishedJob([][]any{{"a", 1}, {"b", 2}}, 5, "p2"),
		"p2": finishedJob([][]any{{"c", 3}, {"d", 4}}, 5, "p3"),
		"p3": finishedJob([][]any{{"e", 5}}, 5, ""),
	}
	client, _ := jobServer(t, func(r *http.Request) (int, any) {
		if r.Method == http.MethodPost {
			return 202, accepted()
		}
		return 200, pages[r.URL.Query().Get("cursor")]
	})

	result, err := client.Run(context.Background(), Request{Metrics: []string{"m"}}, fastPoll())
	if err != nil {
		t.Fatal(err)
	}
	if result.RowCount() != 5 {
		t.Fatalf("want 5 rows across pages, got %d", result.RowCount())
	}
	var got []string
	for _, row := range result.Rows {
		got = append(got, row[0].(string))
	}
	if strings.Join(got, "") != "abcde" {
		t.Errorf("pages arrived out of order or incomplete: %v", got)
	}
}

// TestRunStopsPagingOnceEveryRowIsCollected: a server that keeps handing out a
// cursor must not loop the client. RowCount bounds the walk, so it does not
// depend on trusting the cursor to eventually come back empty.
func TestRunStopsPagingOnceEveryRowIsCollected(t *testing.T) {
	client, _ := jobServer(t, func(r *http.Request) (int, any) {
		if r.Method == http.MethodPost {
			return 202, accepted()
		}
		return 200, finishedJob([][]any{{"a", 1}}, 1, "never-ends")
	})

	result, err := client.Run(context.Background(), Request{Metrics: []string{"m"}}, fastPoll())
	if err != nil {
		t.Fatal(err)
	}
	if result.RowCount() != 1 {
		t.Errorf("the client kept paging past the end: %d rows", result.RowCount())
	}
}

// TestDeniedRequestFailsAtSubmit is why compilation is synchronous on the
// engine: a denial arrives immediately rather than after a wait.
func TestDeniedRequestFailsAtSubmit(t *testing.T) {
	client, calls := jobServer(t, func(*http.Request) (int, any) {
		return 403, map[string]any{"code": "access_denied", "reason": "no", "retry": "never"}
	})

	_, err := client.Run(context.Background(), Request{Metrics: []string{"m"}}, fastPoll())

	var refusal *Refused
	if !errors.As(err, &refusal) || !refusal.IsFinal() {
		t.Fatalf("want a final refusal, got %v", err)
	}
	if len(*calls) != 1 {
		t.Errorf("a denied request must not be polled: %v", *calls)
	}
}

func TestFailedJobCarriesItsRetryClass(t *testing.T) {
	client, _ := jobServer(t, func(r *http.Request) (int, any) {
		if r.Method == http.MethodPost {
			return 202, accepted()
		}
		// A failed job polls as 200: the poll succeeded, the query did not.
		return 200, map[string]any{
			"job_id": "j-1", "state": "failed",
			"code": "execution_failed", "reason": "warehouse unavailable", "retry": "later",
		}
	})

	_, err := client.Run(context.Background(), Request{Metrics: []string{"m"}}, fastPoll())

	var refusal *Refused
	if !errors.As(err, &refusal) {
		t.Fatalf("want a refusal, got %T: %v", err, err)
	}
	if !refusal.ShouldWait() {
		t.Errorf("an executor failure is worth retrying unchanged, got %q", refusal.Retry)
	}
}

func TestCancelledJobIsNotAResult(t *testing.T) {
	client, _ := jobServer(t, func(r *http.Request) (int, any) {
		if r.Method == http.MethodPost {
			return 202, accepted()
		}
		return 200, map[string]any{"job_id": "j-1", "state": "cancelled"}
	})

	if _, err := client.Run(context.Background(), Request{Metrics: []string{"m"}}, fastPoll()); err == nil {
		t.Fatal("a cancelled job must not be reported as a result")
	}
}

// TestCancellingTheContextCancelsTheJob. Giving up must stop the warehouse
// work, not just stop looking: leaving it running keeps spending on an answer
// nobody will read.
func TestCancellingTheContextCancelsTheJob(t *testing.T) {
	var cancelled atomic.Bool
	client, _ := jobServer(t, func(r *http.Request) (int, any) {
		switch r.Method {
		case http.MethodPost:
			return 202, accepted()
		case http.MethodDelete:
			cancelled.Store(true)
			return 200, map[string]any{"job_id": "j-1", "state": "cancelled"}
		default:
			return 200, map[string]any{"job_id": "j-1", "state": "running"}
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := client.Run(ctx, Request{Metrics: []string{"m"}}, fastPoll())
	if err == nil {
		t.Fatal("an expired context must fail the run")
	}
	// Give the fire-and-forget cancellation a moment to land.
	deadline := time.Now().Add(2 * time.Second)
	for !cancelled.Load() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !cancelled.Load() {
		t.Error("abandoning the wait must cancel the job it abandoned")
	}
}

func TestJobPagingArgumentsReachTheServer(t *testing.T) {
	client, calls := jobServer(t, func(*http.Request) (int, any) {
		return 200, finishedJob([][]any{{"a", 1}}, 0, "")
	})

	if _, err := client.Job(context.Background(), "j-1", "p2", 250); err != nil {
		t.Fatal(err)
	}
	call := (*calls)[0]
	if !strings.Contains(call, "cursor=p2") || !strings.Contains(call, "page_size=250") {
		t.Errorf("paging arguments were not sent: %s", call)
	}
}

func TestCancelSendsDelete(t *testing.T) {
	client, calls := jobServer(t, func(*http.Request) (int, any) {
		return 200, map[string]any{"job_id": "j-1", "state": "cancelled"}
	})

	job, err := client.CancelJob(context.Background(), "j-1")
	if err != nil {
		t.Fatal(err)
	}
	if job.State != StateCancelled {
		t.Errorf("want cancelled, got %q", job.State)
	}
	if !strings.HasPrefix((*calls)[0], "DELETE ") {
		t.Errorf("cancel must send DELETE, sent %s", (*calls)[0])
	}
}

func TestJobIDIsRequired(t *testing.T) {
	client, _ := jobServer(t, func(*http.Request) (int, any) { return 200, map[string]any{} })
	if _, err := client.Job(context.Background(), "", "", 0); err == nil {
		t.Error("an empty job id must be rejected")
	}
	if _, err := client.CancelJob(context.Background(), ""); err == nil {
		t.Error("an empty job id must be rejected")
	}
}
