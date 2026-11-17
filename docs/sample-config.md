# Sample configuration: certificates, auth, HTTP headers, gRPC over mTLS

A complete, runnable example lives in `examples/workspace/applications/sample-config`. This page walks through it file by file so you can copy the pattern for your own application.

```
applications/sample-config/
  application.yaml                    environments and the default auth profile
  assets/
    certs/                            certificate and key files (PEM)
      demo-client.crt                 client certificate
      demo-client.key                 client private key
      demo-ca.crt                     CA that signed the server's certificate
    auth/                             one file per auth profile
      client-cert.yaml                mutual TLS
      api-key-over-mtls.yaml          API key header + client certificate
      oauth-client.yaml               OAuth2 client credentials
      basic-user.yaml                 username and password (template)
      static-token.yaml               fixed bearer token (template)
      trust-only.yaml                 trusts the CA, presents no certificate (for negative tests)
  scenarios/
    headers-and-mtls.yaml             HTTP with headers, gRPC with metadata, negative cases
```

Run it:

```bash
./bin/testplane demo --workspace examples/workspace &     # also writes the three demo-* certificate files
./bin/testplane validate --workspace examples/workspace --app sample-config
./bin/testplane run --workspace examples/workspace --app sample-config
```

The demo system listens on `http://127.0.0.1:8089` (REST and token endpoint), `https://localhost:8091` (HTTPS that demands a client certificate) and `localhost:8092` (gRPC that demands a client certificate and `x-tenant` metadata).

## 1. Certificates

Certificates and keys are plain PEM files in the application's `assets/certs` folder. Three files matter:

| File | Needed | What it is |
| --- | --- | --- |
| Client certificate | for mutual TLS | issued to the test client by a CA the service trusts |
| Client private key | for mutual TLS | the key for that certificate, unencrypted PEM |
| CA bundle | only when the service's own certificate comes from a private CA | the CA certificate(s) the test client should trust |

Three ways to put them in place:

- **UI:** Auth & assets, then "Upload certificate or key"; or use the Upload button next to each file in an mTLS profile.
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

## 2. Auth profiles

Each file in `assets/auth` is one way to authenticate. A test picks one with `auth: <id>`; otherwise it inherits from the scenario, then from the application's `defaultAuth`. `auth: none` sends nothing.

**Mutual TLS** (`client-cert.yaml`):

```yaml
id: client-cert
name: Client certificate (mutual TLS)
type: mtls
tls:
  cert: demo-client.crt     # file names in assets/certs
  key: demo-client.key
  ca: demo-ca.crt           # only for a private CA
```

**A header credential plus the certificate** (`api-key-over-mtls.yaml`). The `tls` block can be added to any type:

```yaml
id: api-key-over-mtls
name: API key + client certificate
type: api_key
apiKey:
  name: x-api-key
  value: "{{$env.ORDERS_API_KEY}}"   # the sample uses a literal demo value
  in: header                         # or: query
tls:
  cert: demo-client.crt
  key: demo-client.key
  ca: demo-ca.crt
```

**OAuth2 client credentials** (`oauth-client.yaml`). The token is fetched once per run and sent as `Authorization: Bearer …`:

```yaml
id: oauth-client
name: OAuth2 client credentials
type: oauth2_client_credentials
oauth2:
  tokenUrl: "{{baseUrl}}/oauth/token"
  clientId: demo-client
  clientSecret: "{{$env.ORDERS_CLIENT_SECRET}}"
```

**Basic** and **bearer** (`basic-user.yaml`, `static-token.yaml`):

```yaml
type: basic
basic: {username: qa-user, password: "{{$env.QA_PASSWORD}}"}
---
type: bearer
bearer: {token: "{{$env.API_TOKEN}}"}
```

`{{$env.NAME}}` reads an environment variable of the backend (or of the pipeline job) so the secret is never in the file. In the UI the same profiles are created under Auth & assets; "Try it" loads the certificates and fetches a real token.

## 3. HTTP test with headers

From `scenarios/headers-and-mtls.yaml`:

```yaml
- id: http-with-headers
  name: HTTP call with headers over mutual TLS
  type: http
  # no auth line: inherits the application's defaultAuth (client-cert)
  http:
    method: GET
    url: "{{secureUrl}}/secure/echo"
    headers:
      X-Tenant: "{{tenant}}"
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
      auth: trust-only
      expect:
        error: certificate            # the call fails before any response
```

How headers combine:

- The auth profile adds its headers first (for example `Authorization` or `x-api-key`).
- Headers on the step are added on top and win when the names match.
- A header set to `""` is removed, including one the profile would have sent.
- A case (variant) only lists the headers that differ from its parent.

## 4. gRPC test with metadata over mutual TLS

gRPC's headers are called metadata:

```yaml
- id: grpc-with-metadata
  name: gRPC call with metadata over mutual TLS
  type: grpc
  auth: api-key-over-mtls             # x-api-key metadata + the client certificate
  grpc:
    target: "{{grpcSecureTarget}}"    # host:port; the host must match the server certificate
    method: grpc.health.v1.Health/Check
    metadata:
      x-tenant: "{{tenant}}"
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
      grpc:
        metadata: {x-tenant: ""}
      expect: {code: INVALID_ARGUMENT}
    - id: no-client-certificate
      name: Connection refused without a client certificate
      auth: trust-only
      expect: {error: certificate}
```

Points that matter for gRPC over TLS:

- Leave `plaintext` out (or `false`). TLS is then used, with the client certificate and CA from the auth profile. `plaintext: true` is only for servers without TLS.
- The host in `target` must match a name in the server's certificate. Use the profile's `tls.serverName` when you connect by another name or by IP address.
- Headers from the auth profile become metadata (lower-cased). `Authorization` from an OAuth2 or bearer profile works the same way.
- The service description comes from server reflection. If the service has reflection turned off, upload its `.proto` files to `assets/protos` and list them under `protoFiles`.

## 5. Adapting it to your application

1. Copy the `sample-config` folder under a new name and change `id` and `name` in `application.yaml`.
2. Replace the environment values with your addresses.
3. Put your certificate files in `assets/certs` and update the file names in the auth profiles.
4. Replace literal secrets with `{{$env.NAME}}` references.
5. Edit the scenario's URLs, methods, headers and expectations, then run `validate` and `run`.
