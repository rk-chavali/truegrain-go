// Package truegrain is the Go client for a truegrain semantic engine.
//
// truegrain compiles an Apache Ossie semantic model into governed warehouse
// SQL. It answers questions phrased as metrics, dimensions and structured
// filters, and refuses the ones that would return a plausible wrong number.
// See https://github.com/rk-chavali/truegrain.
//
// This package depends only on the standard library. It is deliberately a
// separate module from the engine: importing the engine would pull in the
// BigQuery client, an OIDC verifier and an MCP server, none of which a caller
// asking for a number needs.
//
// # Getting started
//
//	client, err := truegrain.FromEnv()   // SEMANTIC_URL, SEMANTIC_TOKEN
//	if err != nil {
//		return err
//	}
//
//	result, err := client.Query(ctx, truegrain.Request{
//		Metrics:    []string{"sales.order_revenue"},
//		Dimensions: []string{"sales.customers.region"},
//		Filters:    []truegrain.Filter{truegrain.Eq("sales.orders.status", "shipped")},
//	})
//
// Every [Result] carries CompiledSQL and ModelVersion. That is how a
// disagreement about a number gets settled: two results with the same digest
// were produced by exactly the same definitions.
//
// # There is no way to send SQL
//
// No method on [Client] accepts a SQL string, because no endpoint on the engine
// accepts one. The only expressible request is a semantic one, which is what
// makes an ungoverned query impossible rather than merely discouraged.
//
// # Refusals are answers
//
// The engine refuses questions it cannot answer correctly. A refusal is not a
// failure of the request so much as a statement about it, and it says what to
// do next. Branch on that rather than on the message:
//
//	result, err := client.Run(ctx, req)
//	var refusal *truegrain.Refused
//	if errors.As(err, &refusal) {
//		switch {
//		case refusal.ShouldModify():
//			// Answerable, but not as written. refusal.Hint names the metrics
//			// defined at the grain where the question is well defined.
//		case refusal.ShouldWait():
//			// Nothing about the request is wrong. The same call may work shortly.
//		case refusal.IsFinal():
//			// A denial. Say so rather than substituting a different metric that
//			// answers a different question.
//		}
//	}
//
// That distinction is the difference between a self-correcting agent loop and
// an infinite one.
//
// # Slow warehouses
//
// A warehouse query can run longer than an HTTP request should be held open.
// [Client.Run] submits the query, polls with backoff, collects every page and
// returns one [Result], so no caller writes that loop:
//
//	result, err := client.Run(ctx, truegrain.Request{
//		Metrics: []string{"sales.order_revenue"},
//	})
//
// Cancelling the context cancels the job on the engine as well, rather than
// leaving the warehouse spending on an answer nobody is waiting for.
package truegrain
