package engine

import (
	"context"
	"fmt"
	"sort"

	"github.com/testplane/testplane/internal/auth"
	"github.com/testplane/testplane/internal/model"
	"github.com/testplane/testplane/internal/store"
)

// AuthCheck reports whether a profile can produce credentials right now.
type AuthCheck struct {
	OK         bool         `json:"ok"`
	Error      string       `json:"error,omitempty"`
	Sends      []string     `json:"sends,omitempty"` // names only, never values
	ClientCert bool         `json:"clientCert"`
	CertChecks []auth.Check `json:"certChecks,omitempty"`
}

// CheckAuth loads a profile's certificates and, for OAuth2 profiles,
// fetches a token from the real token endpoint.
func (e *Engine) CheckAuth(ctx context.Context, appID, profileID, envName string) (*AuthCheck, error) {
	app, err := e.Store.GetApp(appID)
	if err != nil {
		return nil, err
	}
	p, err := e.Store.GetAuth(appID, profileID)
	if err != nil {
		return nil, err
	}
	if envName == "" {
		envName = app.DefaultEnvironment
	}
	vars := map[string]string{}
	if envName != "" {
		env, ok := app.Environments[envName]
		if !ok {
			return nil, fmt.Errorf("%w: application %q has no environment %q", store.ErrInvalid, appID, envName)
		}
		for k, v := range env {
			vars[k] = v
		}
	}
	certsDir := e.Store.AssetDir(appID, "certs")
	out := &AuthCheck{CertChecks: auth.CertChecks(p, certsDir)}
	run := &scenarioRun{e: e, app: app, sc: &model.Scenario{}, vars: vars, resolver: auth.NewResolver(certsDir)}
	creds, err := run.credentials(ctx, profileID)
	if err != nil {
		out.Error = err.Error()
		return out, nil
	}
	out.OK = true
	for k := range creds.Headers {
		out.Sends = append(out.Sends, "header "+k)
	}
	for k := range creds.Query {
		out.Sends = append(out.Sends, "query parameter "+k)
	}
	sort.Strings(out.Sends)
	out.ClientCert = creds.TLS != nil && len(creds.TLS.Certificates) > 0
	return out, nil
}
