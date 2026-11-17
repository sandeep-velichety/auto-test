package engine

import (
	"context"
	"net"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/testplane/testplane/internal/demo"
	"github.com/testplane/testplane/internal/model"
	"github.com/testplane/testplane/internal/store"
)

// HTTP headers and gRPC metadata over mutual TLS, with credentials coming
// from an API key profile that also carries the client certificate.
func TestHeadersAndMetadataOverMutualTLS(t *testing.T) {
	certs, err := demo.NewCerts()
	must(t, err)
	cfg, err := certs.ServerTLS()
	must(t, err)
	web := httptest.NewUnstartedServer(demo.SecureHandler())
	web.TLS = cfg
	web.StartTLS()
	defer web.Close()
	cfg2, _ := certs.ServerTLS()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	must(t, err)
	gs := demo.SecureGRPCServer(cfg2)
	go gs.Serve(lis)
	defer gs.Stop()
	_, port, _ := net.SplitHostPort(lis.Addr().String())

	st := store.New(t.TempDir())
	must(t, st.SaveApp(&model.Application{ID: "shop", Name: "Shop", DefaultEnvironment: "t", DefaultAuth: "cert",
		Environments: map[string]map[string]string{"t": {"secureUrl": web.URL, "grpcTarget": "localhost:" + port, "tenant": "acme"}}}))
	must(t, certs.WriteClientFiles(st.AssetDir("shop", "certs")))
	tlsFiles := &model.TLSSettings{Cert: "demo-client.crt", Key: "demo-client.key", CA: "demo-ca.crt"}
	must(t, st.SaveAuth("shop", &model.AuthProfile{ID: "cert", Type: model.AuthMTLS, TLS: tlsFiles}))
	must(t, st.SaveAuth("shop", &model.AuthProfile{ID: "key-and-cert", Type: model.AuthAPIKey, APIKey: &model.APIKeyAuth{Name: "X-Api-Key", Value: "k-1"}, TLS: tlsFiles}))
	must(t, st.SaveAuth("shop", &model.AuthProfile{ID: "trust-only", Type: model.AuthNone, TLS: &model.TLSSettings{CA: "demo-ca.crt"}}))

	health := func() *model.GRPCStep {
		return &model.GRPCStep{Target: "{{grpcTarget}}", Method: "grpc.health.v1.Health/Check", Metadata: map[string]string{"x-tenant": "{{tenant}}"}, Message: map[string]any{"service": "orders"}}
	}
	save(t, st, &model.Scenario{ID: "secure", Variables: map[string]string{"rid": "req-1"}, Tests: []model.Test{
		{ID: "http", Type: model.TypeHTTP, Auth: "key-and-cert",
			HTTP: &model.HTTPStep{URL: "{{secureUrl}}/secure/echo", Headers: map[string]string{"X-Tenant": "{{tenant}}", "X-Request-Id": "{{rid}}"}},
			Expect: &model.Expect{Headers: map[string]string{"x-request-id": "req-1"}, Body: []model.Assertion{
				{Path: "client", Value: "testplane-demo-client"}, {Path: "headers.X-Tenant", Value: "acme"}, {Path: "headers.X-Api-Key", Value: "k-1"}}},
			Variants: []model.Variant{
				{ID: "no-header", HTTP: map[string]any{"headers": map[string]any{"X-Tenant": ""}}, Expect: &model.Expect{Status: intp(400)}},
				{ID: "no-profile-header", HTTP: map[string]any{"headers": map[string]any{"X-Api-Key": ""}}, Expect: &model.Expect{Body: []model.Assertion{{Path: "headers.X-Api-Key", Op: "not_exists"}}}, Kind: model.KindPositive},
				{ID: "no-cert", Auth: "trust-only", Expect: &model.Expect{Error: "certificate"}},
			}},
		{ID: "grpc", Type: model.TypeGRPC, Auth: "key-and-cert", GRPC: health(),
			Expect: &model.Expect{Code: "OK", Body: []model.Assertion{{Path: "status", Value: "SERVING"}}},
			Variants: []model.Variant{
				{ID: "no-metadata", GRPC: map[string]any{"metadata": map[string]any{"x-tenant": ""}}, Expect: &model.Expect{Code: "INVALID_ARGUMENT"}},
				{ID: "no-cert", Auth: "trust-only", Expect: &model.Expect{Error: "certificate"}},
				{ID: "untrusted-server", Auth: "none", Expect: &model.Expect{Error: "certificate"}},
				{ID: "plaintext-to-tls", GRPC: map[string]any{"plaintext": true}},
			}},
		{ID: "grpc-default-auth", Type: model.TypeGRPC, GRPC: health()},
	}})
	res, err := New(st).Run(context.Background(), Options{App: "shop"})
	must(t, err)
	want := map[string]string{"http": "passed", "http.no-header": "passed", "http.no-profile-header": "passed", "http.no-cert": "passed",
		"grpc": "passed", "grpc.no-metadata": "passed", "grpc.no-cert": "passed", "grpc.untrusted-server": "passed", "grpc.plaintext-to-tls": "error", "grpc-default-auth": "passed"}
	got := statuses(res)
	for id, w := range want {
		if got[id] != w {
			t.Errorf("%s: %s, want %s (%s)", id, got[id], w, strings.TrimSpace(find(t, res, id).Error))
		}
	}
}
