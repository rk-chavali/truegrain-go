package truegrain

// Filter is a structured predicate.
//
// It is never a SQL fragment. That is what keeps an ungoverned predicate
// inexpressible, and it is why these are constructors rather than a string
// builder: a misspelled operator is caught at the call site rather than as a
// refusal from the server.
type Filter struct {
	// Dimension is `dataset.field`, or `namespace.dataset.field`.
	Dimension string `json:"dimension"`
	// Op is the comparison. Use the constructors rather than setting it.
	Op string `json:"op"`
	// Values holds one value for the comparisons, two for between, one or more
	// for in and not_in, and none for the null checks.
	Values []any `json:"values,omitempty"`
}

// Eq matches rows where the dimension equals value.
func Eq(dimension string, value any) Filter {
	return Filter{Dimension: dimension, Op: "eq", Values: []any{value}}
}

// Ne matches rows where the dimension does not equal value.
func Ne(dimension string, value any) Filter {
	return Filter{Dimension: dimension, Op: "ne", Values: []any{value}}
}

// In matches rows where the dimension is any of values.
func In(dimension string, values ...any) Filter {
	return Filter{Dimension: dimension, Op: "in", Values: values}
}

// NotIn matches rows where the dimension is none of values.
func NotIn(dimension string, values ...any) Filter {
	return Filter{Dimension: dimension, Op: "not_in", Values: values}
}

// Gt matches rows where the dimension is greater than value.
func Gt(dimension string, value any) Filter {
	return Filter{Dimension: dimension, Op: "gt", Values: []any{value}}
}

// Gte matches rows where the dimension is greater than or equal to value.
func Gte(dimension string, value any) Filter {
	return Filter{Dimension: dimension, Op: "gte", Values: []any{value}}
}

// Lt matches rows where the dimension is less than value.
func Lt(dimension string, value any) Filter {
	return Filter{Dimension: dimension, Op: "lt", Values: []any{value}}
}

// Lte matches rows where the dimension is less than or equal to value.
func Lte(dimension string, value any) Filter {
	return Filter{Dimension: dimension, Op: "lte", Values: []any{value}}
}

// Between matches rows where the dimension falls within the inclusive range.
//
// The engine refuses a reversed range rather than matching no rows, because an
// empty result that looks like a real answer is worse than an error.
func Between(dimension string, low, high any) Filter {
	return Filter{Dimension: dimension, Op: "between", Values: []any{low, high}}
}

// IsNull matches rows where the dimension has no value.
func IsNull(dimension string) Filter {
	return Filter{Dimension: dimension, Op: "is_null"}
}

// IsNotNull matches rows where the dimension has a value.
func IsNotNull(dimension string) Filter {
	return Filter{Dimension: dimension, Op: "is_not_null"}
}
