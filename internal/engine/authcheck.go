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

// CheckAuth resolves a global profile as a run in that environment would:
// it loads certificates and fetches tokens from the real endpoints. appID
// optionally names the application whose values fill the profile's
// variables; mtlsID optionally names the certificate set to combine it with.
func (e *Engine) CheckAuth(ctx context.Context, appID, profileID, envName, mtlsID string) (*AuthCheck, error) {
	p, err := e.Store.GetAuth(profileID)
	if err != nil {
		return nil, err
	}
	run, err := e.adhoc(appID, envName)
	if err != nil {
		return nil, err
	}
	eff, err := p.ForEnv(run.shared.env)
	if err != nil {
		return nil, err
	}
	out := &AuthCheck{CertChecks: auth.CertChecks(eff, e.Store.CertsDir())}
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
// variables and a credential resolver. Without an application, only global
// values and {{app-id.name}} values are available.
func (e *Engine) adhoc(appID, envName string) (*scenarioRun, error) {
	app := &model.Application{}
	if appID != "" {
		a, err := e.Store.GetApp(appID)
		if err != nil {
			return nil, err
		}
		app = a
	}
	envName, vars, err := e.environment(app, envName)
	if err != nil {
		return nil, err
	}
	return &scenarioRun{e: e, app: app, sc: &model.Scenario{}, vars: vars,
		shared: &runShared{env: envName, resolver: auth.NewResolver(e.Store.CertsDir())}}, nil
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
	authID = firstSet(authID, model.AuthNone)
	if mtlsID == "" {
		mtlsID = run.matchSet(step.Target, authID)
	}
	creds, err := run.credentials(ctx, authID, mtlsID)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", store.ErrInvalid, err)
	}
	out, err := executor.GRPCDescribe(ctx, &step, creds, e.Store.ProtosDir(appID))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", store.ErrInvalid, err)
	}
	return out, nil
}
