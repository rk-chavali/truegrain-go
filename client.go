package truegrain

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Operations maps each OpenAPI operationId to the [Client] method implementing
// it.
//
// TestClientCoversTheSpec asserts this covers every operation in
// spec/openapi.yaml, so an endpoint the engine gains cannot quietly go
// unsupported here.
var Operations = map[string]string{
	"getHealth":       "Health",
	"getModelVersion": "ModelVersion",
	"listNamespaces":  "Namespaces",
	"listMetrics":     "Metrics",
	"describeMetric":  "Metric",
	"listDimensions":  "Dimensions",
	"query":           "Query",
	"compile":         "Compile",
	"submitJob":       "Submit",
	"getJob":          "Job",
	"cancelJob":       "CancelJob",
	"listAudit":       "Audit",
}

// DefaultTimeout bounds a single HTTP call.
const DefaultTimeout = 60 * time.Second

// maxBodyBytes bounds a response. A semantic result is bounded by the engine's
// own row limit; anything past this is a misconfigured endpoint rather than an
// answer, and reading it would exhaust memory.
const maxBodyBytes = 256 << 20

// Client is a connection to one truegrain engine.
//
// It is safe for concurrent use. Build one with [New] or [FromEnv].
type Client struct {
	// BaseURL is where the engine is served, without a trailing slash.
	BaseURL string
	// HTTPClient is used for every request. Replace it to add tracing, a
	// proxy, or a different timeout.
	HTTPClient *http.Client
	// UserAgent identifies the caller. The engine's audit log records it,
	// which is how one agent is told from another.
	UserAgent string

	token string
}

// Option configures a [Client].
type Option func(*Client)

// WithToken sets the bearer token identifying the workload.
//
// Read it from the environment; never hard-code one. A token passed as a
// command-line flag ends up in the process list.
func WithToken(token string) Option {
	return func(c *Client) { c.token = token }
}

// WithHTTPClient replaces the HTTP client, which is how to add a proxy, a
// tracing transport, or a different timeout.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) { c.HTTPClient = hc }
}

// WithUserAgent overrides the identifying header, which is useful when you
// want the engine's audit log to distinguish one agent from another.
func WithUserAgent(ua string) Option {
	return func(c *Client) { c.UserAgent = ua }
}

// New builds a client for the engine at baseURL.
func New(baseURL string, opts ...Option) (*Client, error) {
	if baseURL == "" {
		return nil, errors.New("truegrain: a base URL is required")
	}
	c := &Client{
		BaseURL:    strings.TrimRight(baseURL, "/"),
		HTTPClient: &http.Client{Timeout: DefaultTimeout},
		UserAgent:  "truegrain-go/0.1",
	}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

// FromEnv builds a client from SEMANTIC_URL and SEMANTIC_TOKEN.
//
// Keeping the token in the environment keeps it out of source and out of the
// process list.
func FromEnv(opts ...Option) (*Client, error) {
	base := os.Getenv("SEMANTIC_URL")
	if base == "" {
		return nil, errors.New("truegrain: SEMANTIC_URL is not set")
	}
	if token := os.Getenv("SEMANTIC_TOKEN"); token != "" {
		opts = append([]Option{WithToken(token)}, opts...)
	}
	return New(base, opts...)
}

// ---------- metadata ----------

// Health reports what this deployment enforces, including what it does not.
//
// Worth reading before trusting the deployment with anything sensitive:
// Governance.Note and Executor state the gaps in plain language.
func (c *Client) Health(ctx context.Context) (*Health, error) {
	var out Health
	return &out, c.do(ctx, http.MethodGet, "/v1/health", nil, &out)
}

// ModelVersion returns the workspace digest currently served.
//
// Every [Result] carries this too. Two results with the same digest were
// produced by exactly the same definitions.
func (c *Client) ModelVersion(ctx context.Context) (string, error) {
	var out struct {
		ModelVersion string `json:"model_version"`
	}
	if err := c.do(ctx, http.MethodGet, "/v1/model/version", nil, &out); err != nil {
		return "", err
	}
	return out.ModelVersion, nil
}

// Namespaces lists the namespaces in the workspace, their owners and whether
// each one loaded.
func (c *Client) Namespaces(ctx context.Context) ([]Namespace, error) {
	var out struct {
		Namespaces []Namespace `json:"namespaces"`
	}
	return out.Namespaces, c.do(ctx, http.MethodGet, "/v1/namespaces", nil, &out)
}

// Metrics lists every metric this identity may read.
//
// search is a case-insensitive substring over name and description; pass an
// empty string for all of them.
func (c *Client) Metrics(ctx context.Context, search string) ([]Metric, error) {
	path := "/v1/metrics"
	if search != "" {
		path += "?" + url.Values{"search": {search}}.Encode()
	}
	var out struct {
		Metrics []struct {
			Metric
			Dimensions []string `json:"dimensions"`
		} `json:"metrics"`
	}
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	// A listing returns dimension names; Metric carries them as structs so one
	// type serves both calls and a caller never has to know which produced it.
	metrics := make([]Metric, len(out.Metrics))
	for i, m := range out.Metrics {
		metrics[i] = m.Metric
		for _, name := range m.Dimensions {
			metrics[i].Dimensions = append(metrics[i].Dimensions, Dimension{Name: name})
		}
	}
	return metrics, nil
}

// Metric returns one metric's definition and the dimensions legal for it.
//
// name is qualified as `namespace.metric`, or bare when unambiguous across the
// workspace.
func (c *Client) Metric(ctx context.Context, name string) (*Metric, error) {
	if name == "" {
		return nil, errors.New("truegrain: a metric name is required")
	}
	var out struct {
		Metric
		Dimensions []Dimension `json:"dimensions"`
	}
	if err := c.do(ctx, http.MethodGet, "/v1/metrics/"+url.PathEscape(name), nil, &out); err != nil {
		return nil, err
	}
	metric := out.Metric
	metric.Dimensions = out.Dimensions
	return &metric, nil
}

// Dimensions lists what is available for grouping and filtering.
//
// Pass a metric name to get only the dimensions valid for it, which is almost
// always what you want.
func (c *Client) Dimensions(ctx context.Context, metric string) ([]Dimension, error) {
	path := "/v1/dimensions"
	if metric != "" {
		path += "?" + url.Values{"metric": {metric}}.Encode()
	}
	var out struct {
		Dimensions []Dimension `json:"dimensions"`
	}
	return out.Dimensions, c.do(ctx, http.MethodGet, path, nil, &out)
}

// ---------- the record ----------

// Audit reads the decisions this engine recently made, newest first.
//
// The window an operator reads to answer "why did that agent give up". Pass
// [DecisionRefused] for the usual case; an empty decision returns every kind.
// limit is clamped to the engine's maximum of 200; pass 0 for the default.
//
// Not served unless an operator has named who may read it, because the record
// discloses what other teams query and which fields are protected. An engine
// with no reader configured answers 404 and a caller who is authenticated but
// not a named reader gets 403, both of which arrive here as a [*Refused].
func (c *Client) Audit(ctx context.Context, decision Decision, limit int) ([]AuditEvent, error) {
	q := url.Values{}
	if decision != "" {
		switch decision {
		case DecisionAllowed, DecisionRefused, DecisionDenied, DecisionCompiled, DecisionError:
		default:
			// Caught here rather than sent, so a typo names the mistake instead
			// of coming back as a 400 from a round trip.
			return nil, fmt.Errorf("truegrain: %q is not a decision; use one of "+
				"allowed, refused, denied, compiled, error", decision)
		}
		q.Set("decision", string(decision))
	}
	if limit > 0 {
		if limit > 200 {
			limit = 200
		}
		q.Set("limit", strconv.Itoa(limit))
	}
	path := "/v1/audit"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var out struct {
		Events []AuditEvent `json:"events"`
	}
	return out.Events, c.do(ctx, http.MethodGet, path, nil, &out)
}

// ---------- query ----------

// Query answers a question and returns rows.
//
// It holds the connection open for the whole query. Use [Client.Run] when the
// warehouse might be slow.
//
// A refusal comes back as a [*Refused]; check it with [errors.As] and branch on
// ShouldModify, ShouldWait and IsFinal rather than on the message.
func (c *Client) Query(ctx context.Context, req Request) (*Result, error) {
	if len(req.Metrics) == 0 {
		return nil, errors.New("truegrain: a request needs at least one metric")
	}
	var out Result
	return &out, c.do(ctx, http.MethodPost, "/v1/query", req, &out)
}

// Compile returns the SQL a request compiles to, without running it.
//
// A dry run is still governed: a request you may not run returns a refusal and
// no SQL, and the inspection is audited. It is a way to see what a query would
// do, not a way around the gate.
func (c *Client) Compile(ctx context.Context, req Request) (*Compiled, error) {
	if len(req.Metrics) == 0 {
		return nil, errors.New("truegrain: a request needs at least one metric")
	}
	var out Compiled
	return &out, c.do(ctx, http.MethodPost, "/v1/compile", req, &out)
}

// ---------- asynchronous execution ----------

// RunOptions tunes [Client.Run].
type RunOptions struct {
	// PollInterval is the first gap between polls. It backs off to two seconds.
	// Zero uses a sensible default.
	PollInterval time.Duration
	// PageSize is rows per page while collecting. Lower it when rows are wide.
	PageSize int
}

// Run executes a query that may take longer than an HTTP request should.
//
// It submits the query, polls until it finishes, collects every page and
// returns one [Result], so no caller writes that loop. A large warehouse scan
// routinely outlives the default timeout of an HTTP client, and a synchronous
// call that times out leaves the query running and billable with nobody
// reading it.
//
// The governance gate runs during submission, so a request this identity may
// not make returns a refusal immediately rather than after a wait.
//
// Cancelling ctx cancels the job on the engine too, rather than leaving the
// warehouse spending on an answer nobody is waiting for.
func (c *Client) Run(ctx context.Context, req Request, opts ...RunOptions) (*Result, error) {
	var opt RunOptions
	if len(opts) > 0 {
		opt = opts[0]
	}

	job, err := c.Submit(ctx, req)
	if err != nil {
		return nil, err
	}

	finished, err := c.Wait(ctx, job.ID, opt)
	if err != nil {
		return nil, err
	}
	if err := finished.Err(); err != nil {
		return nil, err
	}

	// Walk the remaining pages. RowCount is the size of the whole result, so it
	// bounds the walk without the loop having to trust the cursor to eventually
	// come back empty.
	rows := append([][]Cell(nil), finished.Rows...)
	cursor := finished.NextCursor
	for cursor != "" && (finished.RowCount == 0 || len(rows) < finished.RowCount) {
		page, err := c.Job(ctx, finished.ID, cursor, opt.PageSize)
		if err != nil {
			return nil, err
		}
		if len(page.Rows) == 0 {
			break
		}
		rows = append(rows, page.Rows...)
		cursor = page.NextCursor
	}
	return finished.Result(rows), nil
}

// Submit starts a query in the background and returns immediately.
//
// The returned [Job] already carries CompiledSQL, because compilation happened
// synchronously. A job coming back at all means the query was authorized, not
// merely that it was accepted.
//
// Prefer [Client.Run] unless you genuinely need to do something else while the
// query runs.
func (c *Client) Submit(ctx context.Context, req Request) (*Job, error) {
	if len(req.Metrics) == 0 {
		return nil, errors.New("truegrain: a request needs at least one metric")
	}
	var out Job
	return &out, c.do(ctx, http.MethodPost, "/v1/jobs", req, &out)
}

// Job polls a job and reads one page of its rows.
//
// cursor comes from a previous page's NextCursor; pass an empty string for the
// first page. pageSize of zero uses the engine's default.
func (c *Client) Job(ctx context.Context, id, cursor string, pageSize int) (*Job, error) {
	if id == "" {
		return nil, errors.New("truegrain: a job id is required")
	}
	q := url.Values{}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	if pageSize > 0 {
		q.Set("page_size", strconv.Itoa(pageSize))
	}
	path := "/v1/jobs/" + url.PathEscape(id)
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var out Job
	return &out, c.do(ctx, http.MethodGet, path, nil, &out)
}

// CancelJob stops a running query. It is idempotent: cancelling a finished job
// returns its existing state rather than an error.
func (c *Client) CancelJob(ctx context.Context, id string) (*Job, error) {
	if id == "" {
		return nil, errors.New("truegrain: a job id is required")
	}
	var out Job
	return &out, c.do(ctx, http.MethodDelete, "/v1/jobs/"+url.PathEscape(id), nil, &out)
}

// Wait polls until a job reaches a terminal state.
//
// It backs off from a fast first poll to a two second ceiling, so a query that
// finishes quickly is not delayed and a slow one is not hammered.
//
// If ctx is cancelled the job is cancelled on the engine before returning, so
// giving up here also stops the warehouse work.
func (c *Client) Wait(ctx context.Context, id string, opts ...RunOptions) (*Job, error) {
	var opt RunOptions
	if len(opts) > 0 {
		opt = opts[0]
	}
	interval := opt.PollInterval
	if interval <= 0 {
		interval = 250 * time.Millisecond
	}

	for {
		job, err := c.Job(ctx, id, "", opt.PageSize)
		if err != nil {
			return nil, err
		}
		if job.Done() {
			return job, nil
		}

		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			// Leaving it running would keep spending warehouse time for an
			// answer this caller has already stopped waiting for. The
			// cancellation uses a fresh context because ctx is already done.
			cancelCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			_, _ = c.CancelJob(cancelCtx, id)
			cancel()
			return nil, ctx.Err()
		case <-timer.C:
		}

		if interval < 2*time.Second {
			interval = min(interval*3/2, 2*time.Second)
		}
	}
}

// ---------- internals ----------

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	full := c.BaseURL + path

	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("truegrain: encoding the request: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, full, reader)
	if err != nil {
		return &TransportError{URL: full, Err: err}
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.UserAgent)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return &TransportError{URL: full, Err: err}
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return &TransportError{URL: full, Err: err}
	}

	if resp.StatusCode == http.StatusUnauthorized {
		return &UnauthorizedError{Message: strings.TrimSpace(string(raw))}
	}

	if resp.StatusCode >= 300 {
		// A refusal arrives as an error status with a JSON body. It is an
		// answer, not a transport failure, so it is returned as *Refused with
		// the code and retry class intact.
		var refusal Refused
		if json.Unmarshal(raw, &refusal) == nil && refusal.Code != "" {
			refusal.Status = resp.StatusCode
			return &refusal
		}
		return &TransportError{
			URL: full,
			Err: fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw))),
		}
	}

	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return &TransportError{URL: full, Err: fmt.Errorf("decoding the response: %w", err)}
	}
	return nil
}
