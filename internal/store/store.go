// Package store reads and writes a workspace on disk.
//
//	<workspace>/applications/<app>/application.yaml
//	<workspace>/applications/<app>/assets/auth/<profile>.yaml
//	<workspace>/applications/<app>/assets/certs/<file>
//	<workspace>/applications/<app>/assets/protos/<file>
//	<workspace>/applications/<app>/scenarios/<scenario>.yaml
//	<workspace>/.runs/<app>/<run>.json
package store

import (
	"archive/zip"
	"bytes"
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

var AssetCategories = []string{"certs", "protos"}

type Store struct{ Root string }

func New(root string) *Store { return &Store{Root: root} }

func invalid(f string, a ...any) error { return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(f, a...)) }

func (s *Store) AppDir(app string) string { return filepath.Join(s.Root, "applications", app) }

// AssetDir returns the directory holding one asset category of an app.
func (s *Store) AssetDir(app, category string) string {
	return filepath.Join(s.AppDir(app), "assets", category)
}

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
	for _, d := range []string{"scenarios", "assets/auth", "assets/certs", "assets/protos"} {
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

// ---- auth profiles ----

func (s *Store) authPath(app, id string) (string, error) {
	if !model.ValidID(app) || !model.ValidID(id) {
		return "", invalid("id %q/%q", app, id)
	}
	return filepath.Join(s.AssetDir(app, "auth"), id+".yaml"), nil
}

func (s *Store) ListAuth(app string) ([]model.AuthProfile, error) {
	if _, err := s.GetApp(app); err != nil {
		return nil, err
	}
	ids, err := listIDs(s.AssetDir(app, "auth"), ".yaml")
	if err != nil {
		return nil, err
	}
	out := []model.AuthProfile{}
	for _, id := range ids {
		a, err := s.GetAuth(app, id)
		if err != nil {
			out = append(out, model.AuthProfile{ID: id, Name: id, Description: "INVALID: " + err.Error()})
			continue
		}
		out = append(out, *a)
	}
	return out, nil
}

func (s *Store) GetAuth(app, id string) (*model.AuthProfile, error) {
	p, err := s.authPath(app, id)
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

func (s *Store) SaveAuth(app string, a *model.AuthProfile) error {
	if _, err := s.GetApp(app); err != nil {
		return err
	}
	if errs := a.Validate(); len(errs) > 0 {
		return invalid("%s", strings.Join(errs, "; "))
	}
	if a.Name == "" {
		a.Name = a.ID
	}
	p, err := s.authPath(app, a.ID)
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

func (s *Store) DeleteAuth(app, id string) error {
	p, err := s.authPath(app, id)
	if err != nil {
		return err
	}
	if err := os.Remove(p); errors.Is(err, fs.ErrNotExist) {
		return ErrNotFound
	} else {
		return err
	}
}

// ---- asset files (certs, protos) ----

type Asset struct {
	Category string    `json:"category"`
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

func validCategory(c string) bool {
	for _, x := range AssetCategories {
		if x == c {
			return true
		}
	}
	return false
}

func (s *Store) assetPath(app, category, name string) (string, error) {
	if !model.ValidID(app) || !validCategory(category) || !model.ValidFileName(name) {
		return "", invalid("asset %s/%s", category, name)
	}
	return filepath.Join(s.AssetDir(app, category), name), nil
}

func (s *Store) ListAssets(app string) ([]Asset, error) {
	if _, err := s.GetApp(app); err != nil {
		return nil, err
	}
	out := []Asset{}
	for _, cat := range AssetCategories {
		entries, err := os.ReadDir(s.AssetDir(app, cat))
		if errors.Is(err, fs.ErrNotExist) {
			continue
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
			a := Asset{Category: cat, Name: e.Name(), Size: info.Size(), Modified: info.ModTime()}
			describe(&a, filepath.Join(s.AssetDir(app, cat), e.Name()))
			out = append(out, a)
		}
	}
	return out, nil
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

func (s *Store) SaveAsset(app, category, name string, r io.Reader) error {
	if _, err := s.GetApp(app); err != nil {
		return err
	}
	p, err := s.assetPath(app, category, name)
	if err != nil {
		return err
	}
	b, err := io.ReadAll(io.LimitReader(r, 10<<20))
	if err != nil {
		return err
	}
	if len(b) == 0 {
		return invalid("file is empty")
	}
	return writeFile(p, b, 0o600)
}

func (s *Store) DeleteAsset(app, category, name string) error {
	p, err := s.assetPath(app, category, name)
	if err != nil {
		return err
	}
	if err := os.Remove(p); errors.Is(err, fs.ErrNotExist) {
		return ErrNotFound
	} else {
		return err
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

// ---- export / import ----

// ExportApp writes the application folder as a zip. Unless includeKeys is
// set, private keys are left out and literal secrets in auth profiles are
// replaced with a placeholder.
func (s *Store) ExportApp(app string, w io.Writer, includeKeys bool) error {
	if _, err := s.GetApp(app); err != nil {
		return err
	}
	zw := zip.NewWriter(w)
	root := s.AppDir(app)
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if !includeKeys && strings.HasPrefix(rel, "assets/certs/") {
			a := Asset{Category: "certs"}
			if info, e := d.Info(); e == nil {
				a.Size = info.Size()
			}
			describe(&a, p)
			if a.Kind == "private-key" {
				return nil
			}
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if !includeKeys && strings.HasPrefix(rel, "assets/auth/") && strings.HasSuffix(rel, ".yaml") {
			var prof model.AuthProfile
			if yaml.UnmarshalWithOptions(b, &prof, yaml.Strict()) == nil {
				masked := prof.Masked()
				if redacted, err := yaml.MarshalWithOptions(masked, yaml.Indent(2), yaml.IndentSequence(true)); err == nil {
					b = bytes.ReplaceAll(redacted, []byte(model.SecretMask), []byte("CHANGE_ME"))
				}
			}
		}
		f, err := zw.Create(app + "/" + rel)
		if err != nil {
			return err
		}
		_, err = f.Write(b)
		return err
	})
	if err != nil {
		return err
	}
	return zw.Close()
}

// ImportApp unpacks a zip produced by ExportApp and returns the app id.
func (s *Store) ImportApp(data []byte) (string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", invalid("not a zip file: %v", err)
	}
	app := ""
	type item struct {
		rel string
		f   *zip.File
	}
	var items []item
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		parts := strings.Split(f.Name, "/")
		if len(parts) < 2 || !model.ValidID(parts[0]) {
			return "", invalid("unexpected entry %q", f.Name)
		}
		if app == "" {
			app = parts[0]
		}
		if parts[0] != app {
			return "", invalid("zip holds more than one application")
		}
		rel := parts[1:]
		ok := false
		switch {
		case len(rel) == 1 && rel[0] == "application.yaml":
			ok = true
		case len(rel) == 2 && rel[0] == "scenarios" && strings.HasSuffix(rel[1], ".yaml") && model.ValidID(strings.TrimSuffix(rel[1], ".yaml")):
			ok = true
		case len(rel) == 3 && rel[0] == "assets" && rel[1] == "auth" && strings.HasSuffix(rel[2], ".yaml") && model.ValidID(strings.TrimSuffix(rel[2], ".yaml")):
			ok = true
		case len(rel) == 3 && rel[0] == "assets" && validCategory(rel[1]) && model.ValidFileName(rel[2]):
			ok = true
		}
		if !ok {
			return "", invalid("unexpected entry %q", f.Name)
		}
		items = append(items, item{strings.Join(rel, "/"), f})
	}
	if app == "" {
		return "", invalid("zip is empty")
	}
	for _, it := range items {
		rc, err := it.f.Open()
		if err != nil {
			return "", err
		}
		b, err := io.ReadAll(io.LimitReader(rc, 10<<20))
		rc.Close()
		if err != nil {
			return "", err
		}
		mode := os.FileMode(0o644)
		if strings.HasPrefix(it.rel, "assets/") {
			mode = 0o600
		}
		if err := writeFile(filepath.Join(s.AppDir(app), filepath.FromSlash(it.rel)), b, mode); err != nil {
			return "", err
		}
	}
	if _, err := s.GetApp(app); err != nil {
		return "", invalid("zip has no valid application.yaml: %v", err)
	}
	return app, nil
}
