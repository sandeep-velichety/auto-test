# Sample configuration: certificates, auth, HTTP headers, gRPC over mTLS

A complete, runnable example lives in `examples/workspace/applications/sample-config`. This page walks through it file by file so you can copy the pattern for your own application.

The model in one paragraph: every application configures its own certificates and auth, each of which can differ per environment. When you create a test you choose two things independently: **auth** (how it gets a token, key or headers) and **mutual TLS** (whether a client certificate is presented, and which one).

```
applications/sample-config/
  application.yaml                    environments (local, staging) and their values
  assets/
    certs/                            certificate and key files (PEM)
      demo-client.crt                 client certificate
      demo-client.key                 client private key
      demo-ca.crt                     CA that signed the server's certificate
    tls/                              certificate sets: which files to present for mutual TLS
      client.yaml                     the client certificate, used automatically for one host
      trust-only.yaml                 trusts the CA, presents nothing (for negative tests)
    auth/                             one file per auth profile
      oauth-client.yaml               token from an identity provider (OAuth2), differs per environment
      login-call.yaml                 token from a custom HTTP call
      tenant-headers.yaml             fixed headers / keys
      okta-client.yaml                Okta template
      forgerock-client.yaml           ForgeRock template
      basic-user.yaml                 username and password (template)
      static-token.yaml               fixed bearer token (template)
  scenarios/
    headers-and-mtls.yaml             HTTP with headers, three kinds of token, gRPC with metadata
```

Run it:

```bash
./bin/testplane demo --workspace examples/workspace &     # also writes the three demo-* certificate files
./bin/testplane validate --workspace examples/workspace --app sample-config
./bin/testplane run --workspace examples/workspace --app sample-config
./bin/testplane run --workspace examples/workspace --app sample-config --env staging
```

The demo system listens on `http://127.0.0.1:8089` (REST, an OAuth2 token endpoint and a custom login), `https://localhost:8091` (HTTPS that demands a client certificate) and `localhost:8092` (gRPC that demands a client certificate and `x-tenant` metadata).

## 1. Certificates

Certificates and keys are plain PEM files in the application's `assets/certs` folder. Three files matter:

| File | Needed | What it is |
| --- | --- | --- |
| Client certificate | for mutual TLS | issued to the test client by a CA the service trusts |
| Client private key | for mutual TLS | the key for that certificate, unencrypted PEM |
| CA bundle | only when the service's own certificate comes from a private CA | the CA certificate(s) the test client should trust |

Three ways to put them in place:

- **UI:** Auth & assets, then "Upload certificate or key"; or use the Upload button next to each file in a certificate set.
- **Files:** copy them into `applications/<app>/assets/certs/` (mode `0600` for keys).
- **Pipeline:** write the key from a CI secret before the run (see `docs/deployment.md`).

For your real services, use the certificates your security team issues. To create a throwaway set for a test environment with OpenSSL:

```bash
# 1. A private CA
openssl req -x509 -newkey rsa:2048 -nodes -days 365 \
  -subj "/CN=Test CA" -keyout ca.key -out ca.crt

# 2. A client key and a certificate signed by that CA
openssl req -newkey rsa:2048 -nodes -subj "/CN=qa-test-client" \
  -keyout client.key -out client.csr
printf 'extendedKeyUsage=clientAuth\n' > client.ext
openssl x509 -req -in client.csr -CA ca.crt -CAkey ca.key -CAcreateserial \
  -days 365 -extfile client.ext -out client.crt

# 3. Put the client files (and the CA the SERVER's certificate chains to) next to the application
cp client.crt client.key ca.crt applications/<app>/assets/certs/
chmod 600 applications/<app>/assets/certs/client.key
```

The service must be configured to trust the CA that signed `client.crt`. If your key is password protected, remove the password first (`openssl pkey -in enc.key -out client.key`); encrypted keys are not supported.

Check the result at any time:

```bash
./bin/testplane validate --workspace <workspace> --app <app>
```

It reports a missing file, a certificate where a key should be, a key that does not match its certificate, and expired certificates.

## 2. Certificate sets

A certificate set tells the application which of those files to present. `assets/tls/client.yaml`:

```yaml
id: client
name: Client certificate
hosts:                       # present automatically on calls to these hosts
  - "{{secureHost}}"         # a variable, so each environment can name its own host
cert: demo-client.crt
key: demo-client.key
ca: demo-ca.crt
environments:                # only what differs in an environment
  staging:
    cert: demo-client.crt    # a real setup names staging's own files here
    key: demo-client.key
```

`assets/tls/trust-only.yaml` has only `ca`: it trusts the server and presents nothing, which is what a negative test needs.

In the UI: Auth & assets, "Certificate sets", + New. Pick the three files (or upload them there), optionally list hosts, and use the environment tabs for values that differ. Each file shows whether it is uploaded, of the right kind and unexpired.

How a test uses it:

| On the test | Result |
| --- | --- |
| nothing | the set whose `hosts` match the call is presented; if none matches, no certificate |
| `mtls: client` | that set is presented |
| `mtls: none` | no certificate, even if a host matches |

## 3. Auth profiles

Each file in `assets/auth` is one way to get credentials. A test picks one with `auth: <id>`; otherwise it inherits from the scenario, then from the application's `defaultAuth`. `auth: none` sends nothing.

**Token from an identity provider**, used as a bearer token (`oauth-client.yaml`). The backend asks the provider for a token before the first test that needs it:

```yaml
id: oauth-client
name: Token from the identity provider
type: oauth2_client_credentials
oauth2:
  tokenUrl: "{{baseUrl}}/oauth/token"
  clientId: demo-client
  clientSecret: "{{$env.ORDERS_CLIENT_SECRET}}"   # the sample uses a literal demo value
environments:
  staging:
    oauth2:
      clientId: demo-client                        # staging's own client goes here
      clientSecret: "{{$env.ORDERS_STAGING_SECRET}}"
```

**Okta** (`okta-client.yaml`) and **ForgeRock** (`forgerock-client.yaml`) are the same kind with those providers' token URLs:

```yaml
type: oauth2_client_credentials
oauth2:
  tokenUrl: "https://{{oktaDomain}}/oauth2/default/v1/token"
  clientId: "{{oktaClientId}}"
  clientSecret: "{{$env.OKTA_CLIENT_SECRET}}"
  scopes: [api.read]
  clientAuth: basic
---
type: oauth2_client_credentials
oauth2:
  tokenUrl: "https://{{forgerockHost}}/am/oauth2/realms/root/realms/{{forgerockRealm}}/access_token"
  clientId: "{{forgerockClientId}}"
  clientSecret: "{{$env.FORGEROCK_CLIENT_SECRET}}"
```

Put `oktaDomain`, `oktaClientId` and so on in each environment of the application, so the same profile works everywhere. These two are templates: they were written from the providers' documented URL patterns and have not been run against a real tenant.

**Token from a custom HTTP call** (`login-call.yaml`), for logins that are not OAuth2:

```yaml
id: login-call
name: Token from a custom login call
type: http_call
httpCall:
  method: POST
  url: "{{baseUrl}}/auth/login"
  headers:
    X-Client-Id: demo-client
  body:
    username: qa-user
    password: "{{$env.QA_PASSWORD}}"      # the sample uses a literal demo value
  tokenPath: data.session.token           # where the token is in the JSON response
  headerName: Authorization               # header that carries it on each test request
  prefix: Bearer
```

**Fixed headers / keys** (`tenant-headers.yaml`):

```yaml
id: tenant-headers
name: Tenant and API key headers
type: headers
headers:
  X-Tenant: "{{tenant}}"
  X-Api-Key: "{{$env.ORDERS_API_KEY}}"    # the sample uses a literal demo value
```

`{{$env.NAME}}` reads an environment variable of the backend (or of the pipeline job), so the secret is never in the file. In the UI the same profiles are created under Auth & assets; "Try it" resolves the profile for the chosen environment and fetches a real token.

## 4. HTTP test with headers

From `scenarios/headers-and-mtls.yaml`:

```yaml
- id: http-with-headers
  name: HTTP call with headers over mutual TLS
  type: http
  auth: tenant-headers                # sends X-Tenant and X-Api-Key
  # no "mtls:" line: the "client" set lists this host, so it is presented
  http:
    method: GET
    url: "{{secureUrl}}/secure/echo"
    headers:
      X-Request-Id: "{{requestId}}"
      Accept: application/json
    query:
      verbose: "true"
  expect:
    status: 200
    headers:                          # response headers
      X-Request-Id: "{{requestId}}"
    body:
      - {path: client, value: testplane-demo-client}
      - {path: headers.X-Tenant, value: "{{tenant}}"}
  variants:
    - id: missing-tenant-header
      name: Rejected without the X-Tenant header
      http:
        headers:
          X-Tenant: ""                # an empty value removes the header
      expect:
        status: 400
    - id: no-client-certificate
      name: TLS handshake refused without a client certificate
      mtls: trust-only
      expect:
        error: "*"                    # the call fails before any response
```

How headers combine:

- The auth profile adds its headers first (for example `Authorization`, `X-Tenant` or `X-Api-Key`).
- Headers on the step are added on top and win when the names match.
- A header set to `""` is removed, including one the profile would have sent.
- A case (variant) only lists the headers that differ from its parent.

`error: "*"` accepts any failure. Use it when a server drops a caller that has no certificate: what the client sees at that moment (a TLS alert, a reset connection, a broken pipe) depends on timing.

## 5. gRPC test with metadata over mutual TLS

gRPC's headers are called metadata:

```yaml
- id: grpc-with-metadata
  name: gRPC call with metadata over mutual TLS
  type: grpc
  auth: tenant-headers                # X-Tenant and X-Api-Key become metadata
  mtls: client                        # mutual TLS is needed: present this certificate set
  grpc:
    target: "{{grpcSecureTarget}}"    # host:port; the host must match the server certificate
    method: grpc.health.v1.Health/Check
    metadata:
      x-request-id: "{{requestId}}"
    message:
      service: orders
  expect:
    code: OK
    body:
      - {path: status, value: SERVING}
  variants:
    - id: missing-tenant-metadata
      name: Rejected without x-tenant metadata
      auth: none
      expect: {code: INVALID_ARGUMENT}
    - id: no-client-certificate
      name: Connection refused without a client certificate
      mtls: trust-only
      expect: {error: "*"}
```

Points that matter for gRPC over TLS:

- Leave `plaintext` out (or `false`). TLS is then used, with the certificate set chosen on the test. `plaintext: true` is only for servers without TLS.
- The host in `target` must match a name in the server's certificate. Use the set's `serverName` when you connect by another name or by IP address.
- Headers from the auth profile become metadata (lower-cased). A bearer token from an identity provider works the same way.
- The service description comes from server reflection. If the service has reflection turned off, upload its `.proto` files to `assets/protos` and list them under `protoFiles`.

## 6. Adapting it to your application

1. Copy the `sample-config` folder under a new name and change `id` and `name` in `application.yaml`.
2. List your environments with their addresses and hosts.
3. Put your certificate files in `assets/certs` and name them in a certificate set, with per-environment files where they differ.
4. Keep the auth profiles you need, point them at your identity provider, and replace literal secrets with `{{$env.NAME}}` references.
5. Edit the scenario's URLs, methods, headers and expectations, then run `validate` and `run` for each environment.
