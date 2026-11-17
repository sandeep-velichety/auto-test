package engine

import (
	"context"
	"fmt"
	"sort"

	"github.com/testplane/testplane/internal/auth"
	"github.com/testplane/testplane/internal/executor"
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

// CheckAuth resolves a profile as a run in that environment would: it loads
// certificates and fetches tokens from the real endpoints. mtlsID optionally
// names the certificate set to combine it with.
func (e *Engine) CheckAuth(ctx context.Context, appID, profileID, envName, mtlsID string) (*AuthCheck, error) {
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
	eff, err := p.ForEnv(envName)
	if err != nil {
		return nil, err
	}
	out := &AuthCheck{CertChecks: auth.CertChecks(eff, certsDir)}
	run := &scenarioRun{e: e, app: app, sc: &model.Scenario{}, vars: vars, shared: &runShared{store: e.Store, appEnv: map[string]map[string]string{}, env: envName, resolver: auth.NewResolver("")}}
	creds, err := run.credentials(ctx, profileID, mtlsID)
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

// adhoc prepares what a single call outside a run needs: the environment's
// variables and a credential resolver.
func (e *Engine) adhoc(appID, envName string) (*scenarioRun, error) {
	app, err := e.Store.GetApp(appID)
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
	return &scenarioRun{e: e, app: app, sc: &model.Scenario{}, vars: vars,
		shared: &runShared{store: e.Store, appEnv: map[string]map[string]string{}, env: envName, resolver: auth.NewResolver("")}}, nil
}

// DescribeGRPC lists the services and methods of a gRPC target, using the
// same variables, auth and certificates a test in that environment would.
func (e *Engine) DescribeGRPC(ctx context.Context, appID, envName, authID, mtlsID string, step model.GRPCStep) ([]executor.GRPCService, error) {
	run, err := e.adhoc(appID, envName)
	if err != nil {
		return nil, err
	}
	rd := newRenderer(run.vars)
	step.Target = rd.str(step.Target)
	if err := rd.err(); err != nil {
		return nil, fmt.Errorf("%w: %v", store.ErrInvalid, err)
	}
	if step.Target == "" {
		return nil, fmt.Errorf("%w: enter the target (host:port) first", store.ErrInvalid)
	}
	if authID == "" {
		authID = run.app.DefaultAuth
	}
	if mtlsID == "" {
		mtlsID = run.matchSet(step.Target)
	}
	creds, err := run.credentials(ctx, firstSet(authID, model.AuthNone), mtlsID)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", store.ErrInvalid, err)
	}
	out, err := executor.GRPCDescribe(ctx, &step, creds, e.Store.AssetDir(appID, "protos"))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", store.ErrInvalid, err)
	}
	return out, nil
}
