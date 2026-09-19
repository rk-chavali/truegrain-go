# truegrain (Go)

The Go client for [truegrain](https://github.com/rk-chavali/truegrain): governed
metrics for agents, notebooks and applications.

[![Go Reference](https://pkg.go.dev/badge/github.com/rk-chavali/truegrain-go.svg)](https://pkg.go.dev/github.com/rk-chavali/truegrain-go)

```bash
go get github.com/rk-chavali/truegrain-go
```

**No dependencies.** Only the standard library, and a CI job proves it rather
than the README asserting it. That is why this is a separate module from the
engine: importing the engine would pull in a BigQuery client, an OIDC verifier
and an MCP server, none of which a caller asking for a number needs.

There is no method that sends SQL, because there is no endpoint that accepts it.
The only expressible request is a semantic one.

## Use it

```go
client, err := truegrain.FromEnv() // SEMANTIC_URL, SEMANTIC_TOKEN
if err != nil {
    return err
}

result, err := client.Query(ctx, truegrain.Request{
    Metrics:    []string{"sales.order_revenue", "marketing.campaign_spend"},
    Dimensions: []string{"sales.orders.order_date"},
    Grain:      "month",
    Filters:    []truegrain.Filter{truegrain.Eq("sales.orders.status", "shipped")},
})
if err != nil {
    return err
}

for _, row := range result.Maps() {
    fmt.Println(row)
}
```

Every `Result` carries `CompiledSQL` and `ModelVersion`. That is how a
disagreement about a number gets settled: two results with the same digest were
produced by exactly the same definitions.

## Queries that outlive an HTTP request

A warehouse query can run longer than a connection should be held open.

```go
result, err := client.Run(ctx, truegrain.Request{
    Metrics: []string{"sales.order_revenue"},
})
```

`Run` submits, polls with backoff, collects every page and returns one result,
so no caller writes that loop. Cancelling `ctx` cancels the job on the engine
too, rather than leaving the warehouse spending on an answer nobody is waiting
for.

## Read the refusal

The engine refuses questions it cannot answer correctly rather than returning a
plausible wrong number. A refusal says what to do about itself, and that is what
to branch on:

```go
result, err := client.Run(ctx, req)

var refusal *truegrain.Refused
if errors.As(err, &refusal) {
    switch {
    case refusal.ShouldModify():
        // Answerable, but not as written. refusal.Hint names the metrics
        // defined at the grain where the question is well defined.
    case refusal.ShouldWait():
        // Nothing about the request is wrong. The same call may work shortly.
    case refusal.IsFinal():
        // A denial. Say so rather than substituting a different metric that
        // answers a different question.
    }
}
```

That distinction is the difference between a self-correcting agent loop and an
infinite one.

## Know what is actually enforced

`Health` reports two different things, and they are not the same:

```go
health, _ := client.Health(ctx)

health.Governance.ColumnLevel      // what this engine enforces before emitting SQL
health.Dialect.ColumnLevelSecurity // what the warehouse enforces by itself
health.EnforcementNotes            // the gaps, in plain language
```

When both are false the engine's gate is the only control, and it applies only
to queries that go through the engine. A caller with direct warehouse
credentials is unaffected by it. Reading `EnforcementNotes` before trusting a
deployment with anything sensitive is worth the two lines.

## Reference documentation

Every exported type and method is documented on
[pkg.go.dev](https://pkg.go.dev/github.com/rk-chavali/truegrain-go), generated
from the doc comments in this repository.

## How this stays in step with the engine

The engine's OpenAPI contract is vendored at `spec/openapi.yaml`, pinned to an
engine commit in `spec/PINNED_AT`.

`covers_spec_test.go` checks this client against it in both directions: an
operation the engine exposes with no method here fails the build, and a method
claiming an operation the spec does not define fails it too. A scheduled job
compares the pin against the engine daily and opens a pull request when the
contract moves.

The client is hand-written rather than generated. A generator produces a
transport and a flat error type; the parts worth having are the ones it cannot
produce, namely the refusal semantics above and a `Run` that submits a job and
polls it.

## Licence

Apache 2.0, matching the engine.
