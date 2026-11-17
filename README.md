# Testplane

A scenario-based test tool any team can plug its applications into. Testers describe an application once (environments, auth profiles, certificates), then build scenarios out of ordered tests. A test is an HTTP call, a gRPC call, a Kafka produce or consume, a Mongo check, a Postgres check or a search of the service's logs in Datadog. Everything is stored as YAML in one folder per application, so it can be reviewed, versioned and handed to another team.

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
| `--publish URL` | send the result to a backend's history (token from `TESTPLANE_TOKEN`) |
| `--by NAME` | who or what started the run; recorded with it |

The workspace can also be set with `TESTPLANE_WORKSPACE`. A ready-made GitHub Actions job is in `.github/workflows/ci.yml`, and a `Dockerfile` is included.

## Backends

A **backend** is one running `testplane serve` with its workspace. It keeps the applications, certificates, keys and secrets, runs the tests itself and stores the run history. Testers never need the certificates or secrets on their own machines: they connect the UI to a backend and work there.

```bash
TESTPLANE_TOKEN=choose-a-long-random-value \
  ./bin/testplane serve --workspace /srv/testplane/qa --addr 0.0.0.0:8080 --retention-days 90
```

- **Connecting:** in the UI, click the backend name at the top of the sidebar, add the backend's address and access token, and switch between backends (for example QA and staging) at any time. Connections are remembered in that browser.
- **Access token:** with `--token` (or `TESTPLANE_TOKEN`) every API call must carry the token. Without it the backend is open to anyone who can reach it.
- **Secrets are write-only:** a secret saved in an auth profile can be used and replaced, but the API returns it as `********`. Key files are never returned, and zip exports leave out keys and replace stored secrets with `CHANGE_ME`.
- **Run history:** every run is stored on the backend with its date, environment, who started it and full results. See it per scenario (in the scenario editor) or per application (Runs tab), filtered by result, environment and date. `--retention-days` deletes old runs.
- **Pipelines:** `testplane run … --publish https://backend` sends a headless run's result to a backend, so it appears in the same history.

## Flows across applications

Keep one application per service for service-level tests, and add a "team" application for scenarios that follow a business flow through several services. Inside any scenario:

- `auth: orders/orders-oauth` uses the auth profile (and certificates) of the `orders` application.
- `{{orders.baseUrl}}` reads a value from the `orders` application's environment.

A flow can then call service A, check service B's database, wait for an event and confirm what the service logged. `examples/workspace/applications/team-flows` shows the pattern.

## Service logs (Datadog)

A `logs` step searches Datadog for what a service logged while the scenario ran, so a test can assert that a line appeared, or that no errors did:

```yaml
- id: no-errors-logged
  name: The orders service logged no errors
  type: logs
  kind: negative                 # passes when nothing matches
  retry: {attempts: 6, delayMs: 5000}
  logs:
    query: "service:orders status:error @orderId:{{orderId}}"
    apiKey: "{{$env.DD_API_KEY}}"
    appKey: "{{$env.DD_APP_KEY}}"
```

The keys are read from the backend's environment and are masked in stored results.

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

The full YAML reference, with Kafka, Mongo and Postgres examples, is in [docs/reference.md](docs/reference.md). Building, hosting, access control, CI/CD and operations are covered in [docs/deployment.md](docs/deployment.md). A worked example of certificates, auth profiles, an HTTP test with headers and a gRPC test over mutual TLS is in [docs/sample-config.md](docs/sample-config.md).

## Security notes

- Start shared backends with `--token`. Without one, the API is open to anyone who can reach the port; `serve` binds to `127.0.0.1` by default for that reason. The token is one shared secret, not per-user accounts, so put the backend behind TLS and, for wider audiences, your own sign-on proxy.
- The UI keeps a backend's token in the browser's local storage.
- Auth profiles are plain YAML. Keep secrets out of them with `{{$env.NAME}}`, which reads an environment variable of the process running the tests, for example a CI secret.
- Key files are written with mode `0600`, and the API never returns their contents.

## Development

```bash
go test ./...                     # unit and integration tests
TESTPLANE_TEST_PG_DSN='postgres://user:pass@localhost:5432/db?sslmode=disable' go test ./...   # adds live Postgres tests
cd web && npm run dev             # UI with hot reload, proxies /api to :8080
```

`web/dist` is committed so `go build ./cmd/testplane` works without Node; `make ui` refreshes it.

### Trying Kafka, Mongo and Postgres locally

```bash
docker compose -f examples/docker-compose.yml up -d
./bin/testplane run --workspace examples/workspace --app data-demo
```

### What has and has not been exercised

HTTP, gRPC (server reflection), OAuth2, mutual TLS and Postgres are covered by tests that talk to real servers. The Kafka and Mongo steps are implemented and compile, but they have not been run against a live broker or database yet. The Datadog logs step is tested against a stand-in server that mimics the Logs Search API, not against a real Datadog account. Treat those three as untested until you have tried them in your environment.
