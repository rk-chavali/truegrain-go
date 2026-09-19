package truegrain

import "time"

// Cell is one value in a result row. The engine returns JSON, so a cell is
// whatever JSON gave: a string, a float64, a bool, or nil.
type Cell any

// Request is a semantic question.
//
// It is the same shape for [Client.Query], [Client.Compile], [Client.Submit]
// and [Client.Run]. There is no field for SQL, by design.
type Request struct {
	// Metrics names what to measure. Metrics at different grains, or in
	// different namespaces, are aggregated separately and joined on the shared
	// dimensions rather than refused.
	Metrics []string `json:"metrics"`
	// Dimensions group the result: `dataset.field`, or
	// `namespace.dataset.field` when a bare name would be ambiguous.
	Dimensions []string `json:"dimensions,omitempty"`
	// Filters are structured predicates, never SQL text. Build them with [Eq],
	// [In], [Between] and the rest.
	Filters []Filter `json:"filters,omitempty"`
	// Grain buckets the selected time dimension: day, week, month, quarter,
	// year and the sub-day units.
	Grain string `json:"grain,omitempty"`
	// Limit caps the rows returned. Zero applies the engine's own default.
	Limit int `json:"limit,omitempty"`
	// OrderBy sorts the result.
	OrderBy []Order `json:"order_by,omitempty"`
}

// Order is one sort key.
type Order struct {
	// Field is a metric or dimension name from the same request.
	Field string `json:"field"`
	// Desc sorts descending.
	Desc bool `json:"desc,omitempty"`
}

// Namespace is one team's independently owned set of definitions.
type Namespace struct {
	Name        string   `json:"name"`
	Available   bool     `json:"available"`
	MetricCount int      `json:"metric_count"`
	Digest      string   `json:"digest,omitempty"`
	Owners      []string `json:"owners,omitempty"`
	// Error is set only when the namespace failed to load. An unavailable
	// namespace is reported rather than omitted, so absence is never mistaken
	// for a model that simply had no such metrics.
	Error string `json:"error,omitempty"`
}

// Metric is something the engine can measure.
type Metric struct {
	// Name is qualified as `namespace.metric`.
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	// Description is the grounding text an agent reads to decide whether this
	// metric answers the question asked. It is not decoration.
	Description string   `json:"description"`
	Datatype    string   `json:"datatype,omitempty"`
	Synonyms    []string `json:"synonyms,omitempty"`
	// Definition is the metric's expression. Present on [Client.Metric],
	// absent from a listing.
	Definition string `json:"definition,omitempty"`
	// Dimensions lists only the dimensions this identity may group by.
	Dimensions []Dimension `json:"-"`
}

// Dimension is something a metric can be grouped or filtered by.
type Dimension struct {
	Name        string   `json:"name"`
	Namespace   string   `json:"namespace,omitempty"`
	Datatype    string   `json:"datatype,omitempty"`
	Description string   `json:"description,omitempty"`
	Synonyms    []string `json:"synonyms,omitempty"`
	// IsTime reports whether this dimension accepts a Grain.
	IsTime bool `json:"is_time,omitempty"`
	// Grains are the legal time buckets. Populated only when IsTime.
	Grains []string `json:"grains,omitempty"`
}

// Health describes what a deployment enforces, including what it does not.
//
// Worth reading before trusting a deployment with anything sensitive.
// Overstating a governance guarantee is worse than not offering one, so the
// engine reports its gaps here in plain language.
//
// Two separate things are reported and they are not the same: [Governance]
// says what this engine enforces before it emits SQL, and [DialectSecurity]
// says what the warehouse enforces on its own. A caller with direct warehouse
// credentials is subject only to the second.
type Health struct {
	// Workspace names the namespaces served.
	Workspace string `json:"workspace"`
	// WorkspaceDigest identifies the exact set of definitions in force. Two
	// results carrying the same digest came from the same model.
	WorkspaceDigest string `json:"workspace_digest"`
	// OssieSpecVersion is the Apache Ossie spec version the models declare.
	OssieSpecVersion string `json:"ossie_spec_version,omitempty"`
	// Namespaces reports each namespace and whether it loaded.
	Namespaces []Namespace `json:"namespaces,omitempty"`
	// Dialect describes the compilation target and what it secures itself.
	Dialect DialectSecurity `json:"dialect"`
	// Governance describes the policy resolver this engine applies.
	Governance Governance `json:"governance"`
	// Executor names the warehouse driver and states its limits, including
	// whether queries run as the caller.
	Executor string `json:"executor"`
	// MetricCount and DimensionCount are what this identity may see.
	MetricCount    int `json:"metric_count"`
	DimensionCount int `json:"dimension_count"`
	// SupportedFilterOps are the operators [Filter] may use.
	SupportedFilterOps []string `json:"supported_filter_ops,omitempty"`
	// SupportedGrains are the legal values for Request.Grain.
	SupportedGrains []string `json:"supported_grains,omitempty"`
	// EnforcementNotes states the gaps in plain language. Read these.
	EnforcementNotes []string `json:"enforcement_notes,omitempty"`
}

// DialectSecurity reports what the target warehouse enforces by itself,
// independently of this engine.
//
// When both flags are false, the engine's own gate is the only control, and it
// applies only to queries that go through the engine.
type DialectSecurity struct {
	// Dialect names the compilation target, for example bigquery or duckdb.
	Dialect string `json:"dialect"`
	// ColumnLevelSecurity reports whether the warehouse restricts columns on
	// its own, such as through BigQuery policy tags.
	ColumnLevelSecurity bool `json:"column_level_security"`
	// RowLevelSecurity reports whether the warehouse filters rows on its own.
	RowLevelSecurity bool `json:"row_level_security"`
	// Note states the limits in plain language.
	Note string `json:"note,omitempty"`
}

// Governance describes what this engine's policy resolver enforces.
//
// It is deliberately honest about a resolver that enforces nothing: a
// deployment that grants everything says so here, because overstating a
// guarantee is worse than not offering one.
type Governance struct {
	// Resolver names the implementation, for example bigquery-policy-tags,
	// file, or allow-all.
	Resolver string `json:"resolver"`
	// ColumnLevel reports whether real column-level decisions are made. False
	// means every caller may read every column.
	ColumnLevel bool `json:"column_level"`
	// Note states the limits in plain language.
	Note string `json:"note"`
}

// Compiled is the SQL a request compiles to, without having run it.
type Compiled struct {
	SQL          string   `json:"compiled_sql"`
	Columns      []string `json:"columns"`
	Dialect      string   `json:"dialect"`
	Namespace    string   `json:"namespace"`
	ModelVersion string   `json:"model_version"`
	// Parts is how many facts were aggregated separately and joined. More than
	// one means the query spanned grains or namespaces.
	Parts int `json:"parts"`
}

// Result is rows, plus the provenance needed to defend the numbers in them.
type Result struct {
	Columns []string `json:"columns"`
	Rows    [][]Cell `json:"rows"`
	// CompiledSQL is the exact statement executed. Carried on every response
	// on purpose: it is how a disagreement about a number gets settled.
	CompiledSQL  string `json:"compiled_sql"`
	ModelVersion string `json:"model_version"`
	Namespace    string `json:"namespace"`
	Dialect      string `json:"dialect"`
}

// RowCount is the number of rows in the result.
func (r *Result) RowCount() int { return len(r.Rows) }

// Maps returns every row keyed by column name, which is usually what a caller
// wants to range over.
func (r *Result) Maps() []map[string]Cell {
	out := make([]map[string]Cell, len(r.Rows))
	for i, row := range r.Rows {
		m := make(map[string]Cell, len(r.Columns))
		for j, col := range r.Columns {
			if j < len(row) {
				m[col] = row[j]
			}
		}
		out[i] = m
	}
	return out
}

// JobState is where an asynchronous query has got to.
//
// StateRunning is the only non-terminal one, and StateCancelled is distinct
// from StateFailed because the caller stopped it rather than the warehouse
// failing.
type JobState string

// The states a job can be in.
const (
	StateRunning   JobState = "running"
	StateSucceeded JobState = "succeeded"
	StateFailed    JobState = "failed"
	StateCancelled JobState = "cancelled"
)

// Job is one asynchronously executing query.
type Job struct {
	ID    string   `json:"job_id"`
	State JobState `json:"state"`
	// CompiledSQL is available from the moment of submission, because
	// compilation and the governance gate run before the job exists.
	CompiledSQL string   `json:"compiled_sql,omitempty"`
	Columns     []string `json:"columns,omitempty"`
	// Rows is one page. Use [Client.Run] to collect every page.
	Rows [][]Cell `json:"rows,omitempty"`
	// RowCount is the size of the whole result, not of this page.
	RowCount int `json:"row_count,omitempty"`
	// NextCursor is present when more rows remain. Pass it back to read on.
	NextCursor   string `json:"next_cursor,omitempty"`
	ModelVersion string `json:"model_version,omitempty"`
	Namespace    string `json:"namespace,omitempty"`
	Dialect      string `json:"dialect,omitempty"`

	// The refusal fields, set only when State is StateFailed. They carry the
	// same vocabulary a synchronous refusal does.
	Code   string `json:"code,omitempty"`
	Reason string `json:"reason,omitempty"`
	Hint   string `json:"hint,omitempty"`
	Retry  Retry  `json:"retry,omitempty"`
}

// Done reports whether the job reached any terminal state, including failure.
func (j *Job) Done() bool {
	return j.State == StateSucceeded || j.State == StateFailed || j.State == StateCancelled
}

// Err returns a non-nil error when the job did not succeed.
//
// A failure is returned as a [*Refused] carrying the same code, hint and retry
// class a synchronous refusal would have, so a caller branches on it
// identically.
func (j *Job) Err() error {
	switch j.State {
	case StateSucceeded, StateRunning:
		return nil
	case StateCancelled:
		return &Refused{
			Code:   "job_cancelled",
			Reason: "the job was cancelled before it finished",
			Retry:  RetryLater,
		}
	default:
		retry := j.Retry
		if retry == "" {
			// An executor failure is not a refusal: the request was legal and
			// the warehouse failed. Later is the honest classification.
			retry = RetryLater
		}
		code := j.Code
		if code == "" {
			code = "execution_failed"
		}
		return &Refused{Code: code, Reason: j.Reason, Hint: j.Hint, Retry: retry}
	}
}

// Result renders a finished job's rows as a [Result].
//
// rows overrides the single page held on the job, which is how [Client.Run]
// returns every page as one result.
func (j *Job) Result(rows [][]Cell) *Result {
	if rows == nil {
		rows = j.Rows
	}
	return &Result{
		Columns:      j.Columns,
		Rows:         rows,
		CompiledSQL:  j.CompiledSQL,
		ModelVersion: j.ModelVersion,
		Namespace:    j.Namespace,
		Dialect:      j.Dialect,
	}
}

// AuditEvent is one decision the engine recorded.
//
// Decision is the field to branch on, and Refused and Denied are deliberately
// separate: denied is access, meaning this caller may not read something, and
// refused is correctness, meaning nobody can be told this accurately. Counting
// them together would report every fan-out as an access incident.
//
// Carries no filter values, no compiled SQL and no result rows. SQLHash
// identifies the statement without disclosing it.
type AuditEvent struct {
	Time         time.Time `json:"time"`
	Identity     string    `json:"identity,omitempty"`
	ModelName    string    `json:"model_name,omitempty"`
	ModelVersion string    `json:"model_version,omitempty"`
	Namespace    string    `json:"namespace,omitempty"`
	Metrics      []string  `json:"metrics,omitempty"`
	Dimensions   []string  `json:"dimensions,omitempty"`
	Decision Decision `json:"decision"`
	// RefusalCode names which refusal, for a Decision of DecisionRefused. The
	// same codes [Refused] carries, so one switch serves both.
	RefusalCode string `json:"refusal_code,omitempty"`
	// Retry is what the caller was told to do.
	Retry  Retry  `json:"retry,omitempty"`
	Reason string `json:"reason,omitempty"`
	Hint   string `json:"hint,omitempty"`
	// DeniedFields are the semantic fields withheld, on a denial. Never
	// returned to the denied caller, only to a reader of the record.
	DeniedFields []string `json:"denied_fields,omitempty"`
	SQLHash      string   `json:"sql_hash,omitempty"`
	Dialect      string   `json:"dialect,omitempty"`
	// JobID is the warehouse's own identifier, so two logs can be joined.
	JobID    string `json:"job_id,omitempty"`
	RowCount int    `json:"row_count,omitempty"`
	// BytesBilled is what the warehouse says the query cost. Zero means not
	// reported rather than free: DuckDB bills nobody and reports nothing.
	BytesBilled int64  `json:"bytes_billed,omitempty"`
	DurationMS  int64  `json:"duration_ms,omitempty"`
	Error       string `json:"error,omitempty"`
}

// Decision is what the engine did about one request. Named rather than a bare
// string so a caller switching on it cannot pass a value the engine never
// produces, the same reason [Retry] is a type.
type Decision string

// The decisions an [AuditEvent] can carry. Refused and Denied are separate on
// purpose: see [AuditEvent].
const (
	DecisionAllowed  Decision = "allowed"
	DecisionRefused  Decision = "refused"
	DecisionDenied   Decision = "denied"
	DecisionCompiled Decision = "compiled"
	DecisionError    Decision = "error"
)
