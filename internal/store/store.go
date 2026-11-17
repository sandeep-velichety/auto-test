// Package store reads and writes a workspace on disk.
//
//	<workspace>/global/settings.yaml                      environments, regions, shared values
//	<workspace>/global/auth/<profile>.yaml                auth profiles, for every application
//	<workspace>/global/tls/<certificate set>.yaml         certificate sets, for every application
//	<workspace>/global/certs/<file>                       certificate and key files
//	<workspace>/applications/<app>/application.yaml
//	<workspace>/applications/<app>/assets/protos/<file>
//	<workspace>/applications/<app>/scenarios/<scenario>.yaml
//	<workspace>/.runs/<app>/<run>.json
//
// Older workspaces kept auth, certificate sets and certificate files inside
// each application (assets/auth, assets/tls, assets/certs). Migrate moves
// them to global/.
package store

import (
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/goccy/go-yaml"

	"github.com/testplane/testplane/internal/model"
)

var ErrNotFound = errors.New("not found")
var ErrInvalid = errors.New("invalid")

type Store struct{ Root string }

func New(root string) *Store { return &Store{Root: root} }

func invalid(f string, a ...any) error { return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(f, a...)) }

func (s *Store) AppDir(app string) string { return filepath.Join(s.Root, "applications", app) }

// ProtosDir holds an application's .proto files.
func (s *Store) ProtosDir(app string) string {
	return filepath.Join(s.AppDir(app), "assets", "protos")
}

// GlobalDir holds what every application shares.
func (s *Store) GlobalDir() string { return filepath.Join(s.Root, "global") }

// CertsDir holds the certificate and key files named by certificate sets
// and auth profiles.
func (s *Store) CertsDir() string { return filepath.Join(s.GlobalDir(), "certs") }

func readYAML(path string, v any) error {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if err := yaml.UnmarshalWithOptions(b, v, yaml.Strict()); err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	return nil
}

func writeYAML(path string, v any) error {
	b, err := yaml.MarshalWithOptions(v, yaml.Indent(2), yaml.IndentSequence(true))
	if err != nil {
		return err
	}
	return writeFile(path, b, 0o644)
}

func writeFile(path string, b []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func listIDs(dir, ext string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		name := e.Name()
		if ext == "" {
			if e.IsDir() && model.ValidID(name) {
				ids = append(ids, name)
			}
			continue
		}
		if !e.IsDir() && strings.HasSuffix(name, ext) && model.ValidID(strings.TrimSuffix(name, ext)) {
			ids = append(ids, strings.TrimSuffix(name, ext))
		}
	}
	sort.Strings(ids)
	return ids, nil
}

// ---- global settings ----

func (s *Store) globalPath() string { return filepath.Join(s.GlobalDir(), "settings.yaml") }

// GetGlobal returns the shared environment and region list. A workspace
// without the file gets the list its applications already use, or the
// standard one when there is nothing to go by.
func (s *Store) GetGlobal() (*model.Global, error) {
	var g model.Global
	err := readYAML(s.globalPath(), &g)
	if err == nil {
		return &g, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	apps, err := s.ListApps()
	if err != nil {
		return nil, err
	}
	return globalFromApps(apps), nil
}

// globalFromApps derives the list from the keys applications use.
func globalFromApps(apps []model.Application) *model.Global {
	g := &model.Global{}
	envs, regions := map[string]bool{}, map[string]bool{}
	for _, a := range apps {
		for key := range a.Environments {
			name, region, paired := strings.Cut(key, "/")
			envs[name] = true
			if paired && region != "" {
				regions[region] = true
			}
		}
	}
	if len(envs) == 0 {
		return model.DefaultGlobal()
	}
	// A name used both ways (a region somewhere, an environment elsewhere)
	// stays a region.
	for r := range regions {
		delete(envs, r)
	}
	g.Environments, g.Regions = sortedKeys(envs), sortedKeys(regions)
	if len(g.Environments) == 0 {
		return model.DefaultGlobal()
	}
	for _, a := range apps {
		if g.Has(a.DefaultEnvironment) {
			g.DefaultEnvironment = a.DefaultEnvironment
			break
		}
	}
	if g.DefaultEnvironment == "" {
		g.DefaultEnvironment = g.Keys()[0]
	}
	return g
}

func (s *Store) SaveGlobal(g *model.Global) error {
	if errs := g.Validate(); len(errs) > 0 {
		return invalid("%s", strings.Join(errs, "; "))
	}
	if g.DefaultEnvironment == "" {
		g.DefaultEnvironment = g.Keys()[0]
	}
	for key, vals := range g.Values {
		if len(vals) == 0 {
			delete(g.Values, key)
		}
	}
	return writeYAML(s.globalPath(), g)
}

// ---- applications ----

func (s *Store) ListApps() ([]model.Application, error) {
	ids, err := listIDs(filepath.Join(s.Root, "applications"), "")
	if err != nil {
		return nil, err
	}
	apps := []model.Application{}
	for _, id := range ids {
		a, err := s.GetApp(id)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		apps = append(apps, *a)
	}
	return apps, nil
}

func (s *Store) GetApp(id string) (*model.Application, error) {
	if !model.ValidID(id) {
		return nil, invalid("application id %q", id)
	}
	var a model.Application
	if err := readYAML(filepath.Join(s.AppDir(id), "application.yaml"), &a); err != nil {
		return nil, err
	}
	a.ID = id
	return &a, nil
}

func (s *Store) SaveApp(a *model.Application) error {
	if !model.ValidID(a.ID) {
		return invalid("application id %q must be lowercase letters, digits, '-' or '_'", a.ID)
	}
	if a.Name == "" {
		a.Name = a.ID
	}
	for _, d := range []string{"scenarios", "assets/protos"} {
		if err := os.MkdirAll(filepath.Join(s.AppDir(a.ID), d), 0o755); err != nil {
			return err
		}
	}
	return writeYAML(filepath.Join(s.AppDir(a.ID), "application.yaml"), a)
}

func (s *Store) DeleteApp(id string) error {
	if _, err := s.GetApp(id); err != nil {
		return err
	}
	return os.RemoveAll(s.AppDir(id))
}

// ---- scenarios ----

func (s *Store) scenarioPath(app, id string) (string, error) {
	if !model.ValidID(app) || !model.ValidID(id) {
		return "", invalid("id %q/%q", app, id)
	}
	return filepath.Join(s.AppDir(app), "scenarios", id+".yaml"), nil
}

func (s *Store) ListScenarios(app string) ([]model.Scenario, error) {
	if _, err := s.GetApp(app); err != nil {
		return nil, err
	}
	ids, err := listIDs(filepath.Join(s.AppDir(app), "scenarios"), ".yaml")
	if err != nil {
		return nil, err
	}
	out := []model.Scenario{}
	for _, id := range ids {
		sc, err := s.GetScenario(app, id)
		if err != nil {
			// Keep broken files visible instead of hiding them.
			out = append(out, model.Scenario{ID: id, Name: id, Description: "INVALID: " + err.Error()})
			continue
		}
		out = append(out, *sc)
	}
	return out, nil
}

func (s *Store) GetScenario(app, id string) (*model.Scenario, error) {
	p, err := s.scenarioPath(app, id)
	if err != nil {
		return nil, err
	}
	var sc model.Scenario
	if err := readYAML(p, &sc); err != nil {
		return nil, err
	}
	sc.ID = id
	return &sc, nil
}

func (s *Store) GetScenarioYAML(app, id string) ([]byte, error) {
	p, err := s.scenarioPath(app, id)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotFound
	}
	return b, err
}

func (s *Store) SaveScenario(app string, sc *model.Scenario) error {
	if _, err := s.GetApp(app); err != nil {
		return err
	}
	if errs := sc.Validate(); len(errs) > 0 {
		return invalid("%s", strings.Join(errs, "; "))
	}
	p, err := s.scenarioPath(app, sc.ID)
	if err != nil {
		return err
	}
	return writeYAML(p, sc)
}

// SaveScenarioYAML validates raw YAML and stores it as written, so comments
// and layout survive hand edits.
func (s *Store) SaveScenarioYAML(app, id string, raw []byte) (*model.Scenario, error) {
	if _, err := s.GetApp(app); err != nil {
		return nil, err
	}
	var sc model.Scenario
	if err := yaml.UnmarshalWithOptions(raw, &sc, yaml.Strict()); err != nil {
		return nil, invalid("%v", err)
	}
	if sc.ID == "" {
		sc.ID = id
	}
	if sc.ID != id {
		return nil, invalid("id in YAML (%s) does not match %s", sc.ID, id)
	}
	if errs := sc.Validate(); len(errs) > 0 {
		return nil, invalid("%s", strings.Join(errs, "; "))
	}
	p, err := s.scenarioPath(app, id)
	if err != nil {
		return nil, err
	}
	return &sc, writeFile(p, raw, 0o644)
}

func (s *Store) DeleteScenario(app, id string) error {
	p, err := s.scenarioPath(app, id)
	if err != nil {
		return err
	}
	if err := os.Remove(p); errors.Is(err, fs.ErrNotExist) {
		return ErrNotFound
	} else {
		return err
	}
}

// ---- auth profiles (global) ----

func (s *Store) authPath(id string) (string, error) {
	if !model.ValidID(id) {
		return "", invalid("auth profile id %q", id)
	}
	return filepath.Join(s.GlobalDir(), "auth", id+".yaml"), nil
}

func (s *Store) ListAuth() ([]model.AuthProfile, error) {
	ids, err := listIDs(filepath.Join(s.GlobalDir(), "auth"), ".yaml")
	if err != nil {
		return nil, err
	}
	out := []model.AuthProfile{}
	for _, id := range ids {
		a, err := s.GetAuth(id)
		if err != nil {
			out = append(out, model.AuthProfile{ID: id, Name: id, Description: "INVALID: " + err.Error()})
			continue
		}
		out = append(out, *a)
	}
	return out, nil
}

func (s *Store) GetAuth(id string) (*model.AuthProfile, error) {
	p, err := s.authPath(id)
	if err != nil {
		return nil, err
	}
	var a model.AuthProfile
	if err := readYAML(p, &a); err != nil {
		return nil, err
	}
	a.ID = id
	return &a, nil
}

func (s *Store) SaveAuth(a *model.AuthProfile) error {
	if errs := a.Validate(); len(errs) > 0 {
		return invalid("%s", strings.Join(errs, "; "))
	}
	if a.Name == "" {
		a.Name = a.ID
	}
	p, err := s.authPath(a.ID)
	if err != nil {
		return err
	}
	// Profiles can hold secrets: keep them owner-readable only.
	b, err := yaml.MarshalWithOptions(a, yaml.Indent(2), yaml.IndentSequence(true))
	if err != nil {
		return err
	}
	return writeFile(p, b, 0o600)
}

func (s *Store) DeleteAuth(id string) error {
	p, err := s.authPath(id)
	if err != nil {
		return err
	}
	if err := os.Remove(p); errors.Is(err, fs.ErrNotExist) {
		return ErrNotFound
	} else {
		return err
	}
}

// ---- certificate sets (global) ----

func (s *Store) tlsPath(id string) (string, error) {
	if !model.ValidID(id) {
		return "", invalid("certificate set id %q", id)
	}
	return filepath.Join(s.GlobalDir(), "tls", id+".yaml"), nil
}

func (s *Store) ListCertSets() ([]model.CertSet, error) {
	ids, err := listIDs(filepath.Join(s.GlobalDir(), "tls"), ".yaml")
	if err != nil {
		return nil, err
	}
	out := []model.CertSet{}
	for _, id := range ids {
		c, err := s.GetCertSet(id)
		if err != nil {
			out = append(out, model.CertSet{ID: id, Name: id, Description: "INVALID: " + err.Error()})
			continue
		}
		out = append(out, *c)
	}
	return out, nil
}

func (s *Store) GetCertSet(id string) (*model.CertSet, error) {
	p, err := s.tlsPath(id)
	if err != nil {
		return nil, err
	}
	var c model.CertSet
	if err := readYAML(p, &c); err != nil {
		return nil, err
	}
	c.ID = id
	return &c, nil
}

func (s *Store) SaveCertSet(c *model.CertSet) error {
	if errs := c.Validate(); len(errs) > 0 {
		return invalid("%s", strings.Join(errs, "; "))
	}
	if c.Name == "" {
		c.Name = c.ID
	}
	p, err := s.tlsPath(c.ID)
	if err != nil {
		return err
	}
	return writeYAML(p, c)
}

func (s *Store) DeleteCertSet(id string) error {
	p, err := s.tlsPath(id)
	if err != nil {
		return err
	}
	if err := os.Remove(p); errors.Is(err, fs.ErrNotExist) {
		return ErrNotFound
	} else {
		return err
	}
}

// ---- usage ----

// Usage says which scenarios refer to each auth profile and certificate
// set, as "application/scenario". Because these are shared, it is what a
// change or a delete will touch.
type Usage struct {
	Auth     map[string][]string `json:"auth"`
	CertSets map[string][]string `json:"certSets"`
}

func (s *Store) Usage() (*Usage, error) {
	apps, err := s.ListApps()
	if err != nil {
		return nil, err
	}
	u := &Usage{Auth: map[string][]string{}, CertSets: map[string][]string{}}
	add := func(m map[string][]string, id, where string) {
		if id == "" || id == model.AuthNone {
			return
		}
		for _, w := range m[id] {
			if w == where {
				return
			}
		}
		m[id] = append(m[id], where)
	}
	for _, a := range apps {
		add(u.Auth, a.DefaultAuth, a.ID+" (default)")
		list, err := s.ListScenarios(a.ID)
		if err != nil {
			continue
		}
		for _, sc := range list {
			where := a.ID + "/" + sc.ID
			auths, sets := scenarioRefs(&sc)
			for _, id := range auths {
				add(u.Auth, id, where)
			}
			for _, id := range sets {
				add(u.CertSets, id, where)
			}
		}
	}
	return u, nil
}

// scenarioRefs lists the auth profile and certificate set ids a scenario
// names anywhere, without "none" and without duplicates.
func scenarioRefs(sc *model.Scenario) (auths, sets []string) {
	a, m := map[string]bool{}, map[string]bool{}
	note := func(into map[string]bool, id string) {
		if id != "" && id != model.AuthNone {
			into[id] = true
		}
	}
	note(a, sc.Auth)
	note(m, sc.MTLS)
	for _, t := range sc.Tests {
		note(a, t.Auth)
		note(m, t.MTLS)
		for _, v := range t.Variants {
			note(a, v.Auth)
			note(m, v.MTLS)
		}
	}
	return sortedKeys(a), sortedKeys(m)
}

// ---- files: certificates (global) and protos (per application) ----

type Asset struct {
	Category string    `json:"category"` // certs | protos
	Name     string    `json:"name"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
	Kind     string    `json:"kind,omitempty"`    // certificate | private-key | proto | other
	Subject  string    `json:"subject,omitempty"` // certificates
	Issuer   string    `json:"issuer,omitempty"`
	NotAfter string    `json:"notAfter,omitempty"`
	Expired  bool      `json:"expired,omitempty"`
	IsCA     bool      `json:"isCA,omitempty"`
}

func listFiles(dir, category string) ([]Asset, error) {
	out := []Asset{}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || !model.ValidFileName(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		a := Asset{Category: category, Name: e.Name(), Size: info.Size(), Modified: info.ModTime()}
		describe(&a, filepath.Join(dir, e.Name()))
		out = append(out, a)
	}
	return out, nil
}

func saveFile(dir, name string, r io.Reader) error {
	if !model.ValidFileName(name) {
		return invalid("file name %q", name)
	}
	b, err := io.ReadAll(io.LimitReader(r, 10<<20))
	if err != nil {
		return err
	}
	if len(b) == 0 {
		return invalid("file is empty")
	}
	return writeFile(filepath.Join(dir, name), b, 0o600)
}

func deleteFile(dir, name string) error {
	if !model.ValidFileName(name) {
		return invalid("file name %q", name)
	}
	if err := os.Remove(filepath.Join(dir, name)); errors.Is(err, fs.ErrNotExist) {
		return ErrNotFound
	} else {
		return err
	}
}

// ListCerts lists the shared certificate and key files.
func (s *Store) ListCerts() ([]Asset, error) { return listFiles(s.CertsDir(), "certs") }

func (s *Store) SaveCert(name string, r io.Reader) error { return saveFile(s.CertsDir(), name, r) }

func (s *Store) DeleteCert(name string) error { return deleteFile(s.CertsDir(), name) }

// ListProtos lists an application's .proto files.
func (s *Store) ListProtos(app string) ([]Asset, error) {
	if _, err := s.GetApp(app); err != nil {
		return nil, err
	}
	return listFiles(s.ProtosDir(app), "protos")
}

func (s *Store) SaveProto(app, name string, r io.Reader) error {
	if _, err := s.GetApp(app); err != nil {
		return err
	}
	return saveFile(s.ProtosDir(app), name, r)
}

func (s *Store) DeleteProto(app, name string) error {
	if !model.ValidID(app) {
		return invalid("application id %q", app)
	}
	return deleteFile(s.ProtosDir(app), name)
}

// describe fills in what a file is without ever exposing key material.
func describe(a *Asset, path string) {
	a.Kind = "other"
	if a.Category == "protos" {
		a.Kind = "proto"
		return
	}
	if a.Size > 1<<20 {
		return
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for {
		var blk *pem.Block
		blk, b = pem.Decode(b)
		if blk == nil {
			return
		}
		if strings.Contains(blk.Type, "PRIVATE KEY") {
			a.Kind = "private-key"
			return
		}
		if blk.Type == "CERTIFICATE" {
			a.Kind = "certificate"
			if c, err := x509.ParseCertificate(blk.Bytes); err == nil {
				a.Subject = c.Subject.String()
				a.Issuer = c.Issuer.String()
				a.NotAfter = c.NotAfter.UTC().Format(time.RFC3339)
				a.Expired = time.Now().After(c.NotAfter)
				a.IsCA = c.IsCA
			}
			return
		}
	}
}

// ---- runs ----

func (s *Store) SaveRun(r *model.RunResult) error {
	if !model.ValidID(r.Application) || !model.ValidFileName(r.ID) {
		return invalid("run %q of application %q", r.ID, r.Application)
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(filepath.Join(s.Root, ".runs", r.Application, r.ID+".json"), b, 0o644)
}

func (s *Store) GetRun(app, id string) (*model.RunResult, error) {
	if !model.ValidID(app) || !model.ValidFileName(id) {
		return nil, invalid("run %q", id)
	}
	b, err := os.ReadFile(filepath.Join(s.Root, ".runs", app, id+".json"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var r model.RunResult
	return &r, json.Unmarshal(b, &r)
}

// RunFilter narrows a run listing. Zero values match everything.
type RunFilter struct {
	Scenario    string
	Status      string
	Environment string
	From, To    time.Time
	Limit       int
}

// ListRuns returns runs newest first, without test detail. With a scenario
// filter each entry keeps only that scenario, so its status and duration
// describe the scenario rather than the whole run.
func (s *Store) ListRuns(app string, f RunFilter) ([]model.RunResult, error) {
	if !model.ValidID(app) {
		return nil, invalid("application id %q", app)
	}
	entries, err := os.ReadDir(filepath.Join(s.Root, ".runs", app))
	if errors.Is(err, fs.ErrNotExist) {
		return []model.RunResult{}, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			names = append(names, strings.TrimSuffix(e.Name(), ".json"))
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	out := []model.RunResult{}
	for _, n := range names {
		if f.Limit > 0 && len(out) >= f.Limit {
			break
		}
		r, err := s.GetRun(app, n)
		if err != nil {
			continue
		}
		if !f.From.IsZero() && r.StartedAt.Before(f.From) {
			continue
		}
		if !f.To.IsZero() && r.StartedAt.After(f.To) {
			continue
		}
		if f.Environment != "" && r.Environment != f.Environment {
			continue
		}
		if f.Scenario != "" {
			var only []model.ScenarioResult
			for _, sc := range r.Scenarios {
				if sc.ID == f.Scenario {
					only = append(only, sc)
				}
			}
			if len(only) == 0 {
				continue
			}
			r.Scenarios = only
			r.Status, r.DurationMs, r.Summary = only[0].Status, only[0].DurationMs, model.Summary{}
			for _, t := range only[0].Tests {
				r.Summary.Total++
				switch t.Status {
				case model.StatusPassed:
					r.Summary.Passed++
				case model.StatusSkipped:
					r.Summary.Skipped++
				default:
					r.Summary.Failed++
				}
			}
		}
		if f.Status != "" && r.Status != f.Status {
			continue
		}
		for i := range r.Scenarios {
			r.Scenarios[i].Tests = nil
		}
		out = append(out, *r)
	}
	return out, nil
}

// PruneRuns deletes stored runs that started before the cutoff and returns
// how many were removed.
func (s *Store) PruneRuns(before time.Time) (int, error) {
	root := filepath.Join(s.Root, ".runs")
	apps, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, a := range apps {
		if !a.IsDir() || !model.ValidID(a.Name()) {
			continue
		}
		files, err := os.ReadDir(filepath.Join(root, a.Name()))
		if err != nil {
			continue
		}
		for _, f := range files {
			id := strings.TrimSuffix(f.Name(), ".json")
			if id == f.Name() {
				continue
			}
			r, err := s.GetRun(a.Name(), id)
			if err != nil || !r.StartedAt.Before(before) {
				continue
			}
			if os.Remove(filepath.Join(root, a.Name(), f.Name())) == nil {
				removed++
			}
		}
	}
	return removed, nil
}
