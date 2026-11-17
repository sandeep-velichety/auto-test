package store

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/goccy/go-yaml"

	"github.com/testplane/testplane/internal/model"
)

// BundleKind marks a YAML file that holds a scenario together with the
// auth profiles and certificate sets it uses.
const BundleKind = "testplane/scenario"

// Bundle is "the whole scenario" in one file: enough to recreate it in
// another application or on another backend.
type Bundle struct {
	Kind        string              `yaml:"kind" json:"kind"`
	Application string              `yaml:"application,omitempty" json:"application,omitempty"` // where it came from
	Scenario    model.Scenario      `yaml:"scenario" json:"scenario"`
	Auth        []model.AuthProfile `yaml:"auth,omitempty" json:"auth,omitempty"`
	CertSets    []model.CertSet     `yaml:"certSets,omitempty" json:"certSets,omitempty"`
	Requires    *Requires           `yaml:"requires,omitempty" json:"requires,omitempty"`
}

// Requires lists what the receiving workspace must provide itself.
type Requires struct {
	Variables []string `yaml:"variables,omitempty" json:"variables,omitempty"` // environment values, as {{name}}
	Files     []string `yaml:"files,omitempty" json:"files,omitempty"`         // certificate and proto files
	External  []string `yaml:"external,omitempty" json:"external,omitempty"`   // older exports: assets borrowed from other applications
	Secrets   []string `yaml:"secrets,omitempty" json:"secrets,omitempty"`     // profiles whose secrets were replaced with CHANGE_ME
}

var varRe = regexp.MustCompile(`\{\{\s*([^{}\s]+)\s*\}\}`)

// ExportScenario returns a scenario and the global auth profiles and
// certificate sets it refers to as one YAML document. Secrets are replaced with CHANGE_ME and
// key files are never included.
func (s *Store) ExportScenario(app, id string) ([]byte, error) {
	sc, err := s.GetScenario(app, id)
	if err != nil {
		return nil, err
	}
	a, err := s.GetApp(app)
	if err != nil {
		return nil, err
	}
	b := Bundle{Kind: BundleKind, Application: app, Scenario: *sc, Requires: &Requires{}}
	authIDs, setIDs := map[string]bool{}, map[string]bool{}
	note := func(into map[string]bool, id string) {
		switch {
		case id == "" || id == model.AuthNone:
		case strings.Contains(id, "/"):
			b.Requires.External = append(b.Requires.External, id)
		default:
			into[id] = true
		}
	}
	note(authIDs, sc.Auth)
	note(setIDs, sc.MTLS)
	inheritsDefault := false // some test falls back to the application's default profile
	files := map[string]bool{}
	for _, t := range sc.Tests {
		note(authIDs, t.Auth)
		note(setIDs, t.MTLS)
		inheritsDefault = inheritsDefault || (sc.Auth == "" && t.Auth == "")
		if t.GRPC != nil {
			for _, f := range t.GRPC.ProtoFiles {
				files["protos/"+f] = true
			}
		}
		for _, v := range t.Variants {
			note(authIDs, v.Auth)
			note(setIDs, v.MTLS)
		}
	}
	if inheritsDefault && a.DefaultAuth != "" {
		// Tests that rely on the application's default profile would lose it
		// elsewhere, so the export states it on the scenario.
		note(authIDs, a.DefaultAuth)
		b.Scenario.Auth = a.DefaultAuth
	}
	// Sets that apply by host travel too: the scenario may rely on them.
	if sets, err := s.ListCertSets(); err == nil {
		for _, c := range sets {
			if len(c.Hosts) > 0 {
				setIDs[c.ID] = true
			}
		}
	}
	tlsFiles := func(t *model.TLSSettings) {
		for _, f := range []string{t.Cert, t.Key, t.CA} {
			if f != "" {
				files["certs/"+f] = true
			}
		}
	}
	for _, id := range sortedKeys(authIDs) {
		p, err := s.GetAuth(id)
		if err != nil {
			return nil, fmt.Errorf("auth profile %q used by the scenario: %w", id, err)
		}
		masked := p.Masked()
		if raw, _ := yaml.Marshal(masked); strings.Contains(string(raw), model.SecretMask) {
			b.Requires.Secrets = append(b.Requires.Secrets, id)
		}
		if masked.TLS != nil {
			tlsFiles(masked.TLS)
		}
		for env := range p.Environments {
			if eff, err := p.ForEnv(env); err == nil && eff.TLS != nil {
				tlsFiles(eff.TLS)
			}
		}
		b.Auth = append(b.Auth, masked)
	}
	for _, id := range sortedKeys(setIDs) {
		c, err := s.GetCertSet(id)
		if err != nil {
			return nil, fmt.Errorf("certificate set %q used by the scenario: %w", id, err)
		}
		tlsFiles(c.For(""))
		for env := range c.Environments {
			tlsFiles(c.For(env))
		}
		b.CertSets = append(b.CertSets, *c)
	}
	b.Requires.Files = sortedKeys(files)
	sort.Strings(b.Requires.External)

	// Variables the receiving side must define: everything referenced that
	// the scenario does not set or capture itself.
	body, err := yaml.Marshal(struct {
		S model.Scenario
		A []model.AuthProfile
		C []model.CertSet
	}{b.Scenario, b.Auth, b.CertSets})
	if err != nil {
		return nil, err
	}
	own := map[string]bool{}
	for k := range sc.Variables {
		own[k] = true
	}
	for _, t := range sc.Tests {
		for k := range t.Capture {
			own[k] = true
		}
	}
	need := map[string]bool{}
	for _, m := range varRe.FindAllStringSubmatch(string(body), -1) {
		if n := m[1]; !strings.HasPrefix(n, "$") && !own[n] {
			need[n] = true
		}
	}
	b.Requires.Variables = sortedKeys(need)
	if len(b.Requires.Variables)+len(b.Requires.Files)+len(b.Requires.External)+len(b.Requires.Secrets) == 0 {
		b.Requires = nil
	}
	out, err := yaml.MarshalWithOptions(b, yaml.Indent(2), yaml.IndentSequence(true))
	if err != nil {
		return nil, err
	}
	header := "# Testplane scenario export. Import it with the UI (Import scenario) or\n# \"testplane import --app <id> <file>\". Secrets are replaced with CHANGE_ME.\n"
	return append([]byte(header), []byte(strings.ReplaceAll(string(out), model.SecretMask, "CHANGE_ME"))...), nil
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ImportResult says what an import did.
type ImportResult struct {
	Scenario string    `json:"scenario"`
	Created  []string  `json:"created"`
	Kept     []string  `json:"kept"` // already existed and were left untouched
	Requires *Requires `json:"requires,omitempty"`
	Missing  []string  `json:"missing,omitempty"` // required variables and files this application lacks
}

// ImportScenario stores a scenario from YAML: either a bundle written by
// ExportScenario or a plain scenario file. Auth profiles and certificate
// sets from a bundle become global ones, created only where none with that
// id exists, so existing credentials are never overwritten. An existing
// scenario with the same id is replaced only when overwrite is set.
func (s *Store) ImportScenario(app string, data []byte, overwrite bool) (*ImportResult, error) {
	a, err := s.GetApp(app)
	if err != nil {
		return nil, err
	}
	var probe struct {
		Kind string `yaml:"kind"`
	}
	_ = yaml.Unmarshal(data, &probe)
	var b Bundle
	if probe.Kind == BundleKind {
		if err := yaml.UnmarshalWithOptions(data, &b, yaml.Strict()); err != nil {
			return nil, invalid("%v", err)
		}
	} else {
		if err := yaml.UnmarshalWithOptions(data, &b.Scenario, yaml.Strict()); err != nil {
			return nil, invalid("not a scenario or a scenario export: %v", err)
		}
	}
	sc := &b.Scenario
	if errs := sc.Validate(); len(errs) > 0 {
		return nil, invalid("%s", strings.Join(errs, "; "))
	}
	if _, err := s.GetScenario(app, sc.ID); err == nil && !overwrite {
		return nil, invalid("scenario %q already exists in this application; import with overwrite to replace it", sc.ID)
	}
	for i := range b.Auth {
		if errs := b.Auth[i].Validate(); len(errs) > 0 {
			return nil, invalid("auth profile %q: %s", b.Auth[i].ID, strings.Join(errs, "; "))
		}
	}
	for i := range b.CertSets {
		if errs := b.CertSets[i].Validate(); len(errs) > 0 {
			return nil, invalid("certificate set %q: %s", b.CertSets[i].ID, strings.Join(errs, "; "))
		}
	}
	res := &ImportResult{Scenario: sc.ID, Created: []string{}, Kept: []string{}, Requires: b.Requires}
	for i := range b.Auth {
		p := &b.Auth[i]
		if _, err := s.GetAuth(p.ID); err == nil {
			res.Kept = append(res.Kept, "auth profile "+p.ID)
			continue
		}
		if err := s.SaveAuth(p); err != nil {
			return nil, err
		}
		res.Created = append(res.Created, "auth profile "+p.ID)
	}
	for i := range b.CertSets {
		c := &b.CertSets[i]
		if _, err := s.GetCertSet(c.ID); err == nil {
			res.Kept = append(res.Kept, "certificate set "+c.ID)
			continue
		}
		if err := s.SaveCertSet(c); err != nil {
			return nil, err
		}
		res.Created = append(res.Created, "certificate set "+c.ID)
	}
	if err := s.SaveScenario(app, sc); err != nil {
		return nil, err
	}
	// Exports from before auth became global may say "other-app/profile".
	m := &migration{s: s, keep: true, auth: map[string]map[string]string{}, sets: map[string]map[string]string{}}
	if err := m.rewriteRefs(app); err != nil {
		return nil, err
	}
	if b.Requires != nil {
		env := map[string]string{}
		if g, err := s.GetGlobal(); err == nil {
			name := a.DefaultEnvironment
			if name == "" {
				name = g.DefaultEnvironment
			}
			env = model.LayeredValues(g.Values, name)
			for k, v := range model.LayeredValues(a.Environments, name) {
				env[k] = v
			}
		}
		for _, v := range b.Requires.Variables {
			if _, ok := env[v]; !ok && !strings.Contains(v, ".") {
				res.Missing = append(res.Missing, "variable "+v)
			}
		}
		for _, f := range b.Requires.Files {
			cat, name, _ := strings.Cut(f, "/")
			dir := s.CertsDir()
			if cat == "protos" {
				dir = s.ProtosDir(app)
			}
			if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
				res.Missing = append(res.Missing, "file "+f)
			}
		}
	}
	return res, nil
}
