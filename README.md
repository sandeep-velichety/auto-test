# Testplane

A scenario-based test tool any team can plug its applications into. Testers describe an application once (environments, auth profiles, certificates), then build scenarios out of ordered tests. A test is an HTTP call, a gRPC call, a Kafka produce or consume, a Mongo check or a Postgres check. Everything is stored as YAML in one folder per application, so it can be reviewed, versioned and handed to another team.

Two pieces:

- **Backend** (Go, one binary): runs the tests. It works on its own from a terminal or a CI/CD pipeline and also serves a REST API.
- **Front end** (React): create applications, auth profiles and scenarios, run them and read the results. It is embedded in the binary.

## Quick start

Needs Go 1.24+ and Node 20+.

```bash
make build                                              # builds the UI, then bin/testplane
./bin/testplane demo --workspace examples/workspace &   # sample system to test against
./bin/testplane serve --workspace examples/workspace    # UI on http://127.0.0.1:8080
```

Headless, as a pipeline would run it:

```bash
./bin/testplane validate --workspace examples/workspace
./bin/testplane run --workspace examples/workspace --app orders-demo --junit reports/junit.xml
```

`run` exits with 0 when every test passed, 1 when a test failed and 2 when it could not run (bad workspace, unknown scenario, nothing selected).

| `run` option | Meaning |
| --- | --- |
| `--app ID` | application to run (required) |
| `--env NAME` | environment; default is the application's default |
| `--scenario a,b` | only these scenarios |
| `--tag smoke` | only scenarios carrying one of these tags |
| `--kind positive` | skip negative tests and cases (`negative` skips positive cases instead) |
| `--var name=value` | override a variable, repeatable |
| `--junit FILE`, `--json FILE` | write reports |
| `--save` | store the run in the workspace so the UI lists it |

The workspace can also be set with `TESTPLANE_WORKSPACE`. A ready-made GitHub Actions job is in `.github/workflows/ci.yml`, and a `Dockerfile` is included.

## How a workspace is laid out

```
workspace/
  applications/
    orders-demo/
      application.yaml          name, environments, default auth
      assets/
        auth/orders-oauth.yaml  one file per auth profile
        certs/client.crt        certificates and keys used by profiles
        protos/orders.proto     optional .proto files for gRPC
      scenarios/
        order-lifecycle.yaml    one file per scenario
```

Assets sit next to the application that uses them. To share an application, copy its folder or use "Download as zip" in the UI (private keys are left out of the zip).

## Concepts

**Application.** One system under test. Its environments are sets of variables such as `baseUrl` or `postgresDsn`; tests refer to them as `{{baseUrl}}`, so the same scenarios run against any environment.

**Auth profile.** How a test authenticates. Types: `basic`, `bearer`, `api_key`, `oauth2_client_credentials`, `oauth2_password`, `mtls`, `none`. OAuth2 profiles fetch a token from the token endpoint once per run. Any profile can carry TLS settings, so OAuth2 over mutual TLS is one profile. A test picks its profile; if it does not, it inherits from the scenario, then from the application.

**mTLS.** Choosing the `mtls` type lists what has to be provided and whether each file is in place:

| File | Needed | What it is |
| --- | --- | --- |
| Client certificate | yes | PEM certificate issued to the test client by a CA the service trusts |
| Client private key | yes | Unencrypted PEM key matching that certificate |
| CA bundle | only for a private CA | PEM certificate(s) of the CA that signed the service's certificate |

The same list appears next to the auth selector while editing a test, and `testplane validate` reports missing, mismatched or expired files.

**Scenario.** An ordered list of tests. They run top to bottom. A test can capture values from its result (`capture: {orderId: id}`) and later tests use them (`{{orderId}}`). When a test fails, the tests after it are skipped, because they usually depend on it; set `continueOnFail: true` to carry on.

**Positive and negative tests.** Every test has a `kind`. Negative cases are usually written as **cases** (`variants`) under a positive test: the same call with a few fields overridden and its own expectation. Cases run right after their parent and never feed captures to later steps.

```yaml
- id: create-order
  name: Create an order
  type: http
  http:
    method: POST
    url: "{{baseUrl}}/orders"
    headers: {X-Request-Id: "{{$uuid}}"}
    body: {item: widget, quantity: 2}
  expect:
    status: 201
  capture:
    orderId: id
  variants:
    - id: no-token
      name: Rejected without a token
      auth: none
      expect: {status: 401}
    - id: zero-quantity
      name: Rejected when quantity is zero
      http: {body: {quantity: 0}}     # only the difference
      # no expect: a negative case passes on any non-2xx status
```

**Properties.** Tests and scenarios carry free-form properties (priority, component, owner, requirement id). They show up in results and in the JUnit report.

The full YAML reference, with Kafka, Mongo and Postgres examples, is in [docs/reference.md](docs/reference.md).

## Security notes

- The API has no login. `serve` binds to `127.0.0.1` by default; put it behind your own access control before exposing it.
- Auth profiles are plain YAML. Keep secrets out of them with `{{$env.NAME}}`, which reads an environment variable of the process running the tests, for example a CI secret.
- Key files are written with mode `0600`, and the API never returns their contents.

## Development

```bash
go test ./...                     # unit and integration tests
TESTPLANE_TEST_PG_DSN='postgres://user:pass@localhost:5432/db?sslmode=disable' go test ./...   # adds live Postgres tests
cd web && npm run dev             # UI with hot reload, proxies /api to :8080
```

`web/dist` is committed so `go build ./cmd/testplane` works without Node; `make ui` refreshes it.

### What has and has not been exercised

HTTP, gRPC (server reflection), OAuth2, mutual TLS and Postgres are covered by tests that talk to real servers. The Kafka and Mongo steps are implemented and compile, but they have not been run against a live broker or database yet; treat them as untested until you have tried them in your environment.
