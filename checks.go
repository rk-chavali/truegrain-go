package truegrain

import (
	"context"
	"errors"
	"net/http"
)

// What the engine says about itself, and about the model it is serving.
//
// Everything here is metadata. None of it reads a row, which is why the whole
// file needs only read:model, and why a continuous integration credential that
// checks a model does not also have to be one that can read the warehouse.
//
// Several of these answer 404 rather than an empty success, and the difference
// matters enough to say once: an engine with no test suite is not an engine
// whose tests all pass, an engine that has never reloaded is not an engine in
// which nothing changed, and an engine checking nothing on a timer has no
// history rather than a clean one. Each arrives as a [*Refused] so a caller
// can tell "not configured" from "configured and fine".

// Doctor asks the warehouse whether the model is still true.
//
// Everything else validates the model against itself: the YAML parses, the
// joins resolve, the expressions compile. None of that notices that somebody
// dropped a column last Tuesday, and the first sign of that is usually a
// caller getting an error, which is the most expensive place to find it.
//
// Reports rather than refuses, so a non-nil error here means the check could
// not run. Read [Diagnosis.OK] for the verdict and [Diagnosis.Skipped] for
// the case where nothing could be checked at all.
func (c *Client) Doctor(ctx context.Context) (*Diagnosis, error) {
	var out Diagnosis
	return &out, c.do(ctx, http.MethodGet, "/v1/doctor", nil, &out)
}

// DoctorHistory returns what the scheduled check has seen, oldest first.
//
// Drift is found by looking regularly, not by looking once, which is how
// "when did this start" stays answerable. An engine started without
// -doctor-every has nothing scheduled and answers 404 as a [*Refused].
func (c *Client) DoctorHistory(ctx context.Context) (*DoctorHistory, error) {
	var out DoctorHistory
	return &out, c.do(ctx, http.MethodGet, "/v1/doctor/history", nil, &out)
}

// RunTests asserts what this model answers.
//
// validate says the model holds together and [Client.Diff] says a number
// changed. Neither says a number was ever right.
//
// Check [TestReport.Withheld] as well as OK. A credential without run:query
// cannot cause warehouse execution, so cases that would are withheld and
// counted rather than run or silently dropped, and a caller reading only OK
// would conclude a suite passed when half of it never ran.
func (c *Client) RunTests(ctx context.Context) (*TestReport, error) {
	var out TestReport
	return &out, c.do(ctx, http.MethodPost, "/v1/tests", nil, &out)
}

// Policy reports what this engine enforces, and what it does not.
//
// Says nothing about who is allowed what. For that, and only about yourself,
// use [Client.ExplainPolicy].
func (c *Client) Policy(ctx context.Context) (*Policy, error) {
	var out Policy
	return &out, c.do(ctx, http.MethodGet, "/v1/policy", nil, &out)
}

// ExplainPolicy says what the caller may read of a metric, and why.
//
// Answers "why can I not group by that column" without running a query and
// being denied. For the calling identity only, which is a security property
// rather than a limitation: an endpoint that reported another identity's
// access would publish the policy it was configured to enforce.
//
// metric is the qualified name, as [Client.Metrics] reports it.
func (c *Client) ExplainPolicy(ctx context.Context, metric string) (*PolicyExplanation, error) {
	if metric == "" {
		// Refused here rather than sent, so a caller who forgot the argument
		// reads that instead of a 400 naming a field they did not write.
		return nil, errors.New("truegrain: name the metric to explain")
	}
	var out PolicyExplanation
	body := struct {
		Metric string `json:"metric"`
	}{Metric: metric}
	return &out, c.do(ctx, http.MethodPost, "/v1/policy/explain", body, &out)
}

// Diff reports whether the last model reload moved a number.
//
// This compares the model being served against the one served before it,
// which is the comparison nobody can make from outside the process. An engine
// that has served only one model answers 404 as a [*Refused], because that is
// a different answer from nothing having changed and only one of them is
// reassuring.
func (c *Client) Diff(ctx context.Context) (*Diff, error) {
	var out Diff
	return &out, c.do(ctx, http.MethodGet, "/v1/diff", nil, &out)
}

// Reload tells the engine to re-read its model source, and returns
// [ReloadReading] or [ReloadAlreadyRunning].
//
// It carries no model, deliberately: this means "look now", not "install
// this". The engine already follows git on a timer, so the only thing this
// changes is the wait. A method that accepted a model would be a second way
// into production, one that skips the pull request, the checks and the diff
// that reports which numbers move.
//
// Needs the deploy:model scope, which read:model and run:query never imply.
//
// Returning without error does not mean the new model is serving. A sync is
// not instant, and reporting a commit before the swap happened would be a
// claim a pipeline then asserts as fact. Poll [Client.Health] and read
// Origin.Commit to know when the new model is the one answering.
//
// An engine reading from a path has nothing to re-read and answers 404. A
// model that fails to load is not a failure of this call either: the engine
// keeps serving the previous one and says so in a 502. Both are [*Refused].
func (c *Client) Reload(ctx context.Context) (string, error) {
	var out struct {
		Status string `json:"status"`
	}
	return out.Status, c.do(ctx, http.MethodPost, "/v1/reload", nil, &out)
}
