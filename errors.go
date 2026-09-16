package truegrain

import "fmt"

// Retry is what a caller should do about a refusal.
//
// It is the field an agent branches on. Without it a caller either gives up on
// a typo or loops forever on a denial.
type Retry string

const (
	// RetryModify means the request was understood and cannot be answered as
	// written. Changing the arguments may work; repeating it will not.
	RetryModify Retry = "modify"
	// RetryLater means the same request may succeed later. Something outside
	// the request failed, such as the policy source being unreachable, or this
	// caller is at their concurrency limit.
	RetryLater Retry = "later"
	// RetryNever means no version of this request from this caller will
	// succeed. A denial, or something the model itself has to change.
	RetryNever Retry = "never"
)

// Refused is a structured denial from the engine.
//
// The engine understood the request and declined it, and said what to do about
// that. Use [errors.As] to recover it:
//
//	var refusal *truegrain.Refused
//	if errors.As(err, &refusal) && refusal.ShouldModify() {
//		// adjust the request and try again
//	}
type Refused struct {
	// Code is the machine-readable reason. Branch on this, never on the text.
	Code string `json:"code"`
	// Reason is one sentence stating what was wrong.
	Reason string `json:"reason"`
	// Hint is what to do instead. For a fan-out refusal it names the metrics
	// defined at the grain where the question is well defined.
	Hint string `json:"hint,omitempty"`
	// Retry classifies the refusal. See [Retry].
	Retry Retry `json:"retry"`
	// Status is the HTTP status that carried the refusal, or 0 when the
	// refusal came from a failed job rather than from a response.
	Status int `json:"-"`
}

// Error renders the refusal, including what to do about it.
func (r *Refused) Error() string {
	out := fmt.Sprintf("refused (%s): %s", r.Code, r.Reason)
	if r.Hint != "" {
		out += "\n  hint: " + r.Hint
	}
	return out + "\n  retry: " + string(r.Retry)
}

// ShouldModify reports that the request is answerable, but not as written.
//
// Change the arguments and try again. Repeating it unchanged will not work.
// Hint usually names what to change.
func (r *Refused) ShouldModify() bool { return r.Retry == RetryModify }

// ShouldWait reports that nothing about the request is wrong.
//
// Something outside it failed, such as the policy source being unreachable.
// The same request may succeed shortly.
func (r *Refused) ShouldWait() bool { return r.Retry == RetryLater }

// IsFinal reports that no version of this request from this caller will
// succeed.
//
// Usually a denial. Say so rather than substituting a different metric that
// answers a different question.
func (r *Refused) IsFinal() bool { return r.Retry == RetryNever }

// TransportError means the engine could not be reached, or answered with
// something unparseable.
//
// It is a network or deployment problem, never a statement about the request,
// so retrying the same request is reasonable.
type TransportError struct {
	// URL is what was being called.
	URL string
	// Err is the underlying cause.
	Err error
}

func (e *TransportError) Error() string { return fmt.Sprintf("truegrain: %s: %v", e.URL, e.Err) }

// Unwrap returns the underlying cause.
func (e *TransportError) Unwrap() error { return e.Err }

// UnauthorizedError means credentials are missing or were not recognised.
type UnauthorizedError struct {
	// Message is what the engine said. It never distinguishes an unknown
	// credential from a malformed one, which would let a caller probe for
	// valid tokens.
	Message string
}

func (e *UnauthorizedError) Error() string { return "truegrain: unauthorized: " + e.Message }
