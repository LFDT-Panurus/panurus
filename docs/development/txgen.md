# Tools: txgen (load generator)

`integration/nwo/txgen` is the load generator used by the integration tests and the
benchmarks. It is test-harness code: not part of a deployed Panurus node, and it exports no
node metrics (see the [Metrics Reference](./metrics.md)).

## Unreachable users

`BaseRunner` reads each user's starting balance before a suite and reconciles against it
afterwards. If that read fails the user is dropped rather than aborting the run: the
remaining cases still execute, and a case whose payer or payee has no state is reported as a
failed case.

So the end-of-run reconciliation covers only the users that initialized. Check the
`Found %d new customers` line before trusting a summary.
