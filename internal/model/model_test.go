package model

import (
	"strings"
	"testing"
)

func TestCertSetPerEnvironment(t *testing.T) {
	c := CertSet{ID: "client", Cert: "dev.crt", Key: "dev.key", CA: "ca.pem",
		Environments: map[string]TLSSettings{"staging": {Cert: "stg.crt", Key: "stg.key", ServerName: "orders.stg"}}}
	if got := *c.For("dev"); got != (TLSSettings{Cert: "dev.crt", Key: "dev.key", CA: "ca.pem"}) {
		t.Errorf("environment without an override uses the defaults: %+v", got)
	}
	if got := *c.For("staging"); got != (TLSSettings{Cert: "stg.crt", Key: "stg.key", CA: "ca.pem", ServerName: "orders.stg"}) {
		t.Errorf("override replaces only the fields it sets: %+v", got)
	}
	if errs := c.Validate(); len(errs) != 0 {
		t.Errorf("valid set: %v", errs)
	}
	bad := map[string]CertSet{
		"empty":              {ID: "x"},
		"cert without key":   {ID: "x", Cert: "a.crt"},
		"path in file name":  {ID: "x", Cert: "../a.crt", Key: "a.key"},
		"bad id":             {ID: "Bad Id", Cert: "a.crt", Key: "a.key"},
		"broken only in env": {ID: "x", CA: "ca.pem", Environments: map[string]TLSSettings{"prod": {Cert: "p.crt"}}},
		"path in env file":   {ID: "x", Cert: "a.crt", Key: "a.key", Environments: map[string]TLSSettings{"prod": {CA: "/etc/ca.pem"}}},
	}
	for name, c := range bad {
		if len(c.Validate()) == 0 {
			t.Errorf("%s: should be invalid", name)
		}
	}
	if errs := (&CertSet{ID: "x", CA: "ca.pem", Environments: map[string]TLSSettings{"prod": {Cert: "p.crt"}}}).Validate(); !strings.Contains(strings.Join(errs, ";"), "environment prod") {
		t.Errorf("the message should name the environment: %v", errs)
	}
	if errs := (&CertSet{ID: "ca-only", CA: "ca.pem"}).Validate(); len(errs) != 0 {
		t.Errorf("a CA-only set is valid (trust without a client certificate): %v", errs)
	}
}

func TestAuthProfilePerEnvironment(t *testing.T) {
	p := AuthProfile{ID: "okta", Type: AuthOAuth2ClientCredentials,
		OAuth2:  &OAuth2Auth{TokenURL: "https://dev.okta.example/oauth2/default/v1/token", ClientID: "dev-id", ClientSecret: "dev-secret", Scopes: []string{"api"}},
		Headers: map[string]string{"X-Env": "dev"},
		Environments: map[string]map[string]any{
			"staging": {"oauth2": map[string]any{"tokenUrl": "https://stg.okta.example/oauth2/default/v1/token", "clientId": "stg-id", "clientSecret": "stg-secret"}, "headers": map[string]any{"X-Env": "staging"}},
			"prod":    {"type": "bearer", "bearer": map[string]any{"token": "{{$env.PROD_TOKEN}}"}},
		}}
	dev, err := p.ForEnv("dev")
	if err != nil || dev.OAuth2.ClientID != "dev-id" || dev.Headers["X-Env"] != "dev" || dev.Environments != nil {
		t.Fatalf("dev: %+v %v", dev, err)
	}
	stg, _ := p.ForEnv("staging")
	if stg.OAuth2.ClientID != "stg-id" || stg.OAuth2.ClientSecret != "stg-secret" || stg.OAuth2.Scopes[0] != "api" || stg.Headers["X-Env"] != "staging" || stg.ID != "okta" {
		t.Fatalf("staging merges over the defaults and keeps what it does not mention: %+v", stg.OAuth2)
	}
	prod, _ := p.ForEnv("prod")
	if prod.Type != AuthBearer || prod.Bearer.Token != "{{$env.PROD_TOKEN}}" {
		t.Fatalf("an environment can even switch the type: %+v", prod)
	}
	if p.OAuth2.ClientID != "dev-id" {
		t.Fatal("ForEnv must not change the stored profile")
	}
	if errs := p.Validate(); len(errs) != 0 {
		t.Fatalf("valid: %v", errs)
	}
	p.Environments["qa"] = map[string]any{"type": "basic"}
	if errs := p.Validate(); len(errs) != 1 || !strings.Contains(errs[0], "environment qa") {
		t.Fatalf("an override that breaks the profile is reported with its environment: %v", errs)
	}
}

func TestMaskingCoversHeadersCallsAndEnvironments(t *testing.T) {
	p := AuthProfile{ID: "x", Type: AuthHTTPCall,
		Headers:  map[string]string{"X-Api-Key": "hdr-secret", "X-Ref": "{{$env.REF}}"},
		HTTPCall: &HTTPCallAuth{URL: "https://idp/token", Headers: map[string]string{"X-Client-Secret": "call-secret"}, Form: map[string]string{"password": "form-secret"}, TokenPath: "data.token"},
		Environments: map[string]map[string]any{"staging": {
			"headers":  map[string]any{"X-Api-Key": "stg-hdr-secret"},
			"httpCall": map[string]any{"url": "https://stg-idp/token", "form": map[string]any{"password": "stg-form-secret"}},
		}}}
	m := p.Masked()
	out := profileMap(&m)
	flat := ""
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			for _, c := range x {
				walk(c)
			}
		case string:
			flat += x + "\n"
		}
	}
	walk(out)
	if strings.Contains(flat, "-secret") {
		t.Fatalf("masked profile leaks: %s", flat)
	}
	if m.Headers["X-Ref"] != "{{$env.REF}}" || m.HTTPCall.URL != "https://idp/token" || m.HTTPCall.TokenPath != "data.token" || m.Environments["staging"]["httpCall"].(map[string]any)["url"] != "https://stg-idp/token" {
		t.Fatalf("masking hid too much: %+v", m)
	}
	if p.Headers["X-Api-Key"] != "hdr-secret" {
		t.Fatal("Masked must not change the original")
	}

	// Round trip: masked values keep what is stored; edited ones replace it.
	edited := m
	edited.Headers = map[string]string{"X-Api-Key": SecretMask, "X-New": "new-value"}
	edited.Environments = map[string]map[string]any{"staging": {
		"headers":  map[string]any{"X-Api-Key": "rotated"},
		"httpCall": map[string]any{"url": "https://stg-idp/token", "form": map[string]any{"password": SecretMask}},
	}}
	edited.KeepSecrets(&p)
	if edited.Headers["X-Api-Key"] != "hdr-secret" || edited.Headers["X-New"] != "new-value" || edited.HTTPCall.Form["password"] != "form-secret" || edited.HTTPCall.Headers["X-Client-Secret"] != "call-secret" {
		t.Fatalf("defaults after keep: %+v %+v", edited.Headers, edited.HTTPCall)
	}
	stg := edited.Environments["staging"]
	if stg["headers"].(map[string]any)["X-Api-Key"] != "rotated" || stg["httpCall"].(map[string]any)["form"].(map[string]any)["password"] != "stg-form-secret" {
		t.Fatalf("environment after keep: %+v", stg)
	}
	// An override block copied from the masked defaults keeps the default secret.
	copied := m
	copied.Environments = map[string]map[string]any{"prod": {"headers": map[string]any{"X-Api-Key": SecretMask, "X-Env": "prod"}}}
	copied.KeepSecrets(&p)
	if h := copied.Environments["prod"]["headers"].(map[string]any); h["X-Api-Key"] != "hdr-secret" || h["X-Env"] != "prod" {
		t.Fatalf("masked value in a new override should fall back to the default: %+v", h)
	}
	fresh := AuthProfile{ID: "y", Type: AuthHeaders, Headers: map[string]string{"X-Key": SecretMask}}
	fresh.KeepSecrets(nil)
	if fresh.Headers["X-Key"] != "" {
		t.Fatalf("the mask itself must never be stored: %q", fresh.Headers["X-Key"])
	}
}

func TestMatchesHost(t *testing.T) {
	cases := []struct {
		patterns []string
		host     string
		want     bool
	}{
		{[]string{"orders.example.com"}, "orders.example.com", true},
		{[]string{"orders.example.com"}, "orders.example.com:8443", true},
		{[]string{"orders.example.com:8443"}, "orders.example.com:8443", true},
		{[]string{"orders.example.com:8443"}, "orders.example.com:9443", false},
		{[]string{"orders.example.com:8443"}, "orders.example.com", false},
		{[]string{"Orders.Example.com"}, "orders.example.COM", true},
		{[]string{"*.example.com"}, "orders.example.com", true},
		{[]string{"*.example.com"}, "a.b.example.com:443", true},
		{[]string{"*.example.com"}, "example.com", false},
		{[]string{"*.example.com"}, "evilexample.com", false},
		{[]string{"*.example.com"}, "example.com.evil.net", false},
		{[]string{"billing.example.com", " localhost:8091 "}, "localhost:8091", true},
		{[]string{"localhost"}, "127.0.0.1", false},
		{[]string{""}, "orders.example.com", false},
		{[]string{"orders.example.com"}, "", false},
		{nil, "orders.example.com", false},
	}
	for _, c := range cases {
		if got := MatchesHost(c.patterns, c.host); got != c.want {
			t.Errorf("MatchesHost(%v, %q) = %v, want %v", c.patterns, c.host, got, c.want)
		}
	}
}
