// Command testplane runs the test workspace: as a web server with the UI,
// or headless from a terminal or CI pipeline.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/testplane/testplane/internal/api"
	"github.com/testplane/testplane/internal/auth"
	"github.com/testplane/testplane/internal/demo"
	"github.com/testplane/testplane/internal/engine"
	"github.com/testplane/testplane/internal/model"
	"github.com/testplane/testplane/internal/report"
	"github.com/testplane/testplane/internal/store"
	"github.com/testplane/testplane/web"
)

var version = "0.1.0"

const usage = `testplane - scenario based API, messaging and data test runner

Usage:
  testplane serve     --workspace DIR [--addr :8080] [--token T]   web UI + REST API
  testplane run       --workspace DIR --app ID [options]      run tests headless (CI/CD)
  testplane validate  --workspace DIR [--app ID]              check YAML, global auth and certificates
  testplane migrate   --workspace DIR                         move auth and certificates kept inside applications to global/
  testplane list      --workspace DIR                         list applications and scenarios
  testplane export    --workspace DIR --app ID [--scenario ID] [--out FILE]
                                                              one scenario as YAML, or the application as a zip
  testplane import    --workspace DIR [--app ID] FILE         a scenario YAML into an application, or an application zip
  testplane demo      [--workspace DIR]                       start a sample system to test against
  testplane version

Run "testplane <command> -h" for the options of a command.
The workspace can also be set with TESTPLANE_WORKSPACE.

Exit codes of "run": 0 all tests passed, 1 at least one test failed, 2 could not run.
`

type multi []string

func (m *multi) String() string     { return strings.Join(*m, ",") }
func (m *multi) Set(v string) error { *m = append(*m, v); return nil }

func csv(m multi) []string {
	var out []string
	for _, v := range m {
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

func workspaceFlag(fs *flag.FlagSet) *string {
	def := os.Getenv("TESTPLANE_WORKSPACE")
	if def == "" {
		def = "workspace"
	}
	return fs.String("workspace", def, "workspace directory")
}

// openStore opens a workspace and brings an older layout up to date first
// (auth and certificates used to live inside each application).
func openStore(ws string) *store.Store {
	st := store.New(ws)
	notes, err := st.Migrate()
	if len(notes) > 0 {
		fmt.Fprintln(os.Stderr, "workspace updated: auth and certificates are now global (see global/)")
		for _, n := range notes {
			fmt.Fprintln(os.Stderr, "  "+n)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: updating the workspace:", err)
		os.Exit(2)
	}
	return st
}

func migrateCmd(args []string) int {
	fs := flag.NewFlagSet("migrate", flag.ExitOnError)
	ws := workspaceFlag(fs)
	_ = fs.Parse(args)
	st := store.New(*ws)
	notes, err := st.Migrate()
	for _, n := range notes {
		fmt.Println(n)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 2
	}
	if len(notes) == 0 {
		fmt.Println("nothing to do: auth and certificates are already global")
	}
	return 0
}

func die(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(2)
}

func main() {
	if len(os.Args) < 2 {
		fmt.Print(usage)
		os.Exit(2)
	}
	args := os.Args[2:]
	switch os.Args[1] {
	case "serve":
		serve(args)
	case "run":
		os.Exit(run(args))
	case "migrate":
		os.Exit(migrateCmd(os.Args[2:]))
	case "validate":
		os.Exit(validate(args))
	case "list":
		list(args)
	case "export":
		os.Exit(exportCmd(args))
	case "import":
		os.Exit(importCmd(args))
	case "demo":
		runDemo(args)
	case "version", "--version", "-v":
		fmt.Println(version)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
}

func serve(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	ws := workspaceFlag(fs)
	addr := fs.String("addr", "127.0.0.1:8080", "listen address (the API has no login; bind beyond localhost only on a trusted network)")
	token := fs.String("token", os.Getenv("TESTPLANE_TOKEN"), "access token every API call must present (default $TESTPLANE_TOKEN; empty = no check)")
	retention := fs.Int("retention-days", 0, "delete stored runs older than this many days (0 = keep all)")
	var origins multi
	fs.Var(&origins, "allow-origin", "web origin allowed to call this backend from a browser, e.g. https://tests.example.com; repeat, or * for any (default: any when a token is set)")
	_ = fs.Parse(args)
	if err := os.MkdirAll(filepath.Join(*ws, "applications"), 0o755); err != nil {
		die(err)
	}
	srv := api.New(openStore(*ws), web.FS())
	srv.Token, srv.AllowOrigins, srv.RetentionDays = *token, csv(origins), *retention
	srv.Prune()
	access := "no access token (anyone who can reach this address can use it)"
	if *token != "" {
		access = "access token required"
	}
	fmt.Printf("testplane %s\nworkspace %s\n%s\nlistening on http://%s\n", version, *ws, access, *addr)
	die(http.ListenAndServe(*addr, srv.Handler()))
}

func run(args []string) int {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	ws := workspaceFlag(fs)
	app := fs.String("app", "", "application id (required)")
	env := fs.String("env", "", "environment name (default: the application's default, else the global default)")
	region := fs.String("region", "", "region, when the global list has regions (same as --env ENV/REGION)")
	kind := fs.String("kind", "", "positive = skip negative cases, negative = skip positive variants")
	junit := fs.String("junit", "", "write a JUnit XML report to this file")
	jsonOut := fs.String("json", "", "write the full JSON result to this file")
	save := fs.Bool("save", false, "also store the run in the workspace so the UI lists it")
	quiet := fs.Bool("quiet", false, "print only the summary")
	publish := fs.String("publish", "", "also send the result to this backend so it appears in its history, e.g. https://tests.example.com (token from $TESTPLANE_TOKEN)")
	by := fs.String("by", os.Getenv("USER"), "who or what started this run, recorded with it")
	var scenarios, tags, vars multi
	fs.Var(&scenarios, "scenario", "scenario id; repeat or comma-separate (default: all)")
	fs.Var(&tags, "tag", "run scenarios carrying any of these tags; repeat or comma-separate")
	fs.Var(&vars, "var", "variable override name=value; repeat")
	_ = fs.Parse(args)
	if *app == "" {
		fmt.Fprintln(os.Stderr, "error: --app is required")
		return 2
	}
	if *kind != "" && *kind != model.KindPositive && *kind != model.KindNegative {
		fmt.Fprintln(os.Stderr, "error: --kind must be positive or negative")
		return 2
	}
	overrides := map[string]string{}
	for _, v := range vars {
		k, val, ok := strings.Cut(v, "=")
		if !ok || k == "" {
			fmt.Fprintf(os.Stderr, "error: --var %q must be name=value\n", v)
			return 2
		}
		overrides[k] = val
	}
	st := openStore(*ws)
	if *region != "" {
		name := *env
		if name == "" {
			// No --env: keep the environment of the default pair.
			if a, err := st.GetApp(*app); err == nil {
				name, _, _ = strings.Cut(a.DefaultEnvironment, "/")
			}
			if g, err := st.GetGlobal(); err == nil && name == "" {
				name, _, _ = strings.Cut(g.DefaultEnvironment, "/")
			}
		}
		if name == "" || strings.Contains(name, "/") {
			fmt.Fprintln(os.Stderr, "error: --region needs --env with a plain environment name, e.g. --env stage --region NA")
			return 2
		}
		*env = name + "/" + *region
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	last := ""
	opts := engine.Options{App: *app, Env: *env, Scenarios: csv(scenarios), Tags: csv(tags), Kind: *kind, Vars: overrides, By: *by, Source: "cli"}
	if !*quiet {
		opts.OnTest = func(sc *model.Scenario, t *model.TestResult) {
			if sc.ID != last {
				fmt.Printf("\n%s (%s)\n", sc.Name, sc.ID)
				last = sc.ID
			}
			report.Line(os.Stdout, t)
		}
	}
	res, err := engine.New(st).Run(ctx, opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 2
	}
	report.Summary(os.Stdout, res)
	write := func(path string, fn func(*os.File) error) bool {
		if path == "" {
			return true
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return false
		}
		f, err := os.Create(path)
		if err == nil {
			err = errors.Join(fn(f), f.Close())
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return false
		}
		return true
	}
	ok := write(*junit, func(f *os.File) error { return report.JUnit(f, res) })
	ok = write(*jsonOut, func(f *os.File) error { return report.JSON(f, res) }) && ok
	if *save {
		if err := st.SaveRun(res); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			ok = false
		}
	}
	if *publish != "" {
		if err := publishRun(*publish, os.Getenv("TESTPLANE_TOKEN"), res); err != nil {
			fmt.Fprintln(os.Stderr, "error: publish:", err)
			ok = false
		} else {
			fmt.Printf("published to %s\n", *publish)
		}
	}
	switch {
	case !ok:
		return 2
	case res.Summary.Total == 0:
		fmt.Fprintln(os.Stderr, "error: no tests matched the selection")
		return 2
	case res.Status != model.StatusPassed:
		return 1
	}
	return 0
}

func publishRun(base, token string, res *model.RunResult) error {
	body, err := json.Marshal(res)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(base, "/")+"/api/apps/"+res.Application+"/runs", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
		return fmt.Errorf("backend answered %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}

func validate(args []string) int {
	fs := flag.NewFlagSet("validate", flag.ExitOnError)
	ws := workspaceFlag(fs)
	only := fs.String("app", "", "validate one application (default: all)")
	_ = fs.Parse(args)
	st := openStore(*ws)
	apps, err := st.ListApps()
	if err != nil {
		die(err)
	}
	problems, checked := 0, 0
	report := func(where, msg string) {
		problems++
		fmt.Printf("  PROBLEM %s: %s\n", where, msg)
	}

	// Names are unique within their kind, so people can tell things apart.
	type named struct{ id, name string }
	duplicates := func(where, kind string, list []named) {
		first := map[string]string{}
		for _, n := range list {
			if other, ok := first[n.name]; ok {
				report(where+n.id, fmt.Sprintf("%s name %q is also used by %s; names must be unique", kind, n.name, other))
				continue
			}
			first[n.name] = n.id
		}
	}
	var appNames []named
	for _, a := range apps {
		appNames = append(appNames, named{a.ID, a.Name})
	}

	// Global: the environment list, auth profiles and certificate sets,
	// checked for every environment.
	fmt.Println("global")
	g, err := st.GetGlobal()
	if err != nil {
		report("settings", err.Error())
		g = model.DefaultGlobal()
	}
	for _, e := range g.Validate() {
		report("settings", e)
	}
	envs := append([]string{""}, g.Keys()...)
	profiles, err := st.ListAuth()
	if err != nil {
		die(err)
	}
	known := map[string]bool{model.AuthNone: true}
	var authNames, setNames []named
	for _, p := range profiles {
		known[p.ID] = true
		authNames = append(authNames, named{p.ID, p.Name})
		where := "auth/" + p.ID
		if strings.HasPrefix(p.Description, "INVALID: ") {
			report(where, strings.TrimPrefix(p.Description, "INVALID: "))
			continue
		}
		for _, e := range p.Validate() {
			report(where, e)
		}
		seen := map[string]bool{}
		for _, env := range envs {
			eff, err := p.ForEnv(env)
			if err != nil {
				continue
			}
			for _, c := range auth.CertChecks(eff, st.CertsDir()) {
				msg := fmt.Sprintf("%s (%s): %s %s", c.Label, c.Field, c.Status, c.Detail)
				if c.Status != "ok" && (c.Required || c.Status != "not_set") && !seen[msg] {
					seen[msg] = true
					if env != "" {
						msg = "environment " + env + ": " + msg
					}
					report(where, msg)
				}
			}
		}
	}
	sets, err := st.ListCertSets()
	if err != nil {
		die(err)
	}
	knownSets := map[string]bool{model.AuthNone: true}
	for _, c := range sets {
		knownSets[c.ID] = true
		setNames = append(setNames, named{c.ID, c.Name})
		where := "tls/" + c.ID
		if strings.HasPrefix(c.Description, "INVALID: ") {
			report(where, strings.TrimPrefix(c.Description, "INVALID: "))
			continue
		}
		for _, e := range c.Validate() {
			report(where, e)
		}
		seen := map[string]bool{}
		for _, env := range envs {
			for _, ch := range auth.TLSChecks(c.For(env), false, st.CertsDir()) {
				msg := fmt.Sprintf("%s (%s): %s %s", ch.Label, ch.File, ch.Status, ch.Detail)
				if ch.Status != "ok" && (ch.Required || ch.Status != "not_set") && !seen[msg] {
					seen[msg] = true
					if env != "" {
						msg = "environment " + env + ": " + msg
					}
					report(where, msg)
				}
			}
		}
	}
	duplicates("auth/", "auth profile", authNames)
	duplicates("tls/", "certificate set", setNames)
	duplicates("applications/", "application", appNames)
	fmt.Printf("  %d environment(s)%s, %d auth profile(s), %d certificate set(s)\n", len(g.Environments), regionCount(g), len(profiles), len(sets))

	for _, a := range apps {
		if *only != "" && a.ID != *only {
			continue
		}
		checked++
		fmt.Printf("%s\n", a.ID)
		if a.DefaultAuth != "" && !known[a.DefaultAuth] {
			report("application", "defaultAuth "+a.DefaultAuth+" is not a global auth profile")
		}
		if a.DefaultEnvironment != "" && !g.Has(a.DefaultEnvironment) {
			report("application", "defaultEnvironment "+a.DefaultEnvironment+" is not in the global list ("+strings.Join(g.Keys(), ", ")+")")
		}
		for _, d := range []string{"auth", "tls", "certs"} {
			if left, _ := os.ReadDir(filepath.Join(st.AppDir(a.ID), "assets", d)); len(left) > 0 {
				report("assets/"+d, "files left from before auth and certificates became global; fix them and run \"testplane migrate\"")
			}
		}
		scs, err := st.ListScenarios(a.ID)
		if err != nil {
			die(err)
		}
		tests := 0
		var scNames []named
		for _, sc := range scs {
			scNames = append(scNames, named{sc.ID, sc.Name})
		}
		duplicates("scenarios/", "scenario", scNames)
		for _, sc := range scs {
			where := "scenarios/" + sc.ID
			if strings.HasPrefix(sc.Description, "INVALID: ") {
				report(where, strings.TrimPrefix(sc.Description, "INVALID: "))
				continue
			}
			for _, e := range sc.Validate() {
				report(where, e)
			}
			refs := []string{sc.Auth}
			setRefs := []string{sc.MTLS}
			for _, t := range sc.Tests {
				tests += 1 + len(t.Variants)
				refs = append(refs, t.Auth)
				setRefs = append(setRefs, t.MTLS)
				for _, v := range t.Variants {
					refs = append(refs, v.Auth)
					setRefs = append(setRefs, v.MTLS)
				}
			}
			for _, r := range setRefs {
				if r != "" && !knownSets[r] {
					report(where, "certificate set "+r+" does not exist under global")
				}
			}
			for _, r := range refs {
				if r != "" && !known[r] {
					report(where, "auth profile "+r+" does not exist under global")
				}
			}
		}
		fmt.Printf("  %d scenario(s), %d test(s)\n", len(scs), tests)
	}
	if checked == 0 {
		fmt.Fprintln(os.Stderr, "error: no application found in", *ws)
		return 2
	}
	if problems > 0 {
		fmt.Printf("\n%d problem(s) found\n", problems)
		return 1
	}
	fmt.Println("\nworkspace is valid")
	return 0
}

func regionCount(g *model.Global) string {
	if len(g.Regions) == 0 {
		return ""
	}
	return fmt.Sprintf(" x %d region(s)", len(g.Regions))
}

func list(args []string) {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	ws := workspaceFlag(fs)
	_ = fs.Parse(args)
	st := openStore(*ws)
	apps, err := st.ListApps()
	if err != nil {
		die(err)
	}
	for _, a := range apps {
		fmt.Printf("%s  (%s)\n", a.ID, a.Name)
		scs, err := st.ListScenarios(a.ID)
		if err != nil {
			die(err)
		}
		for _, sc := range scs {
			fmt.Printf("  %-30s %d test(s)  %s\n", sc.ID, len(sc.Tests), strings.Join(sc.Tags, ","))
		}
	}
}

func exportCmd(args []string) int {
	fs := flag.NewFlagSet("export", flag.ExitOnError)
	ws := workspaceFlag(fs)
	app := fs.String("app", "", "application id (required)")
	scenario := fs.String("scenario", "", "export this scenario with the global auth profiles and certificate sets it uses, as YAML (default: the whole application as a zip)")
	out := fs.String("out", "", "file to write (default: <scenario>.scenario.yaml or <app>.zip; - for standard output)")
	_ = fs.Parse(args)
	if *app == "" {
		fmt.Fprintln(os.Stderr, "error: --app is required")
		return 2
	}
	st := openStore(*ws)
	var data []byte
	var err error
	name := *app + ".zip"
	if *scenario != "" {
		name = *scenario + ".scenario.yaml"
		data, err = st.ExportScenario(*app, *scenario)
	} else {
		var buf bytes.Buffer
		err = st.ExportApp(*app, &buf, false)
		data = buf.Bytes()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 2
	}
	if *out == "-" {
		_, _ = os.Stdout.Write(data)
		return 0
	}
	if *out != "" {
		name = *out
	}
	if err := os.WriteFile(name, data, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 2
	}
	fmt.Println("wrote", name)
	return 0
}

func importCmd(args []string) int {
	fs := flag.NewFlagSet("import", flag.ExitOnError)
	ws := workspaceFlag(fs)
	app := fs.String("app", "", "application to import a scenario into (not needed for an application zip)")
	overwrite := fs.Bool("overwrite", false, "update the scenario or application if it already exists")
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "error: give exactly one file to import")
		return 2
	}
	data, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 2
	}
	st := openStore(*ws)
	if bytes.HasPrefix(data, []byte("PK")) {
		id, err := st.ImportApp(data, *overwrite)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			if errors.Is(err, store.ErrConflict) && !*overwrite {
				fmt.Fprintln(os.Stderr, "       add --overwrite to update the existing one")
			}
			return 2
		}
		fmt.Println("imported application", id)
		return 0
	}
	if *app == "" {
		fmt.Fprintln(os.Stderr, "error: --app is required to import a scenario")
		return 2
	}
	res, err := st.ImportScenario(*app, data, *overwrite)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		if errors.Is(err, store.ErrConflict) && !*overwrite {
			fmt.Fprintln(os.Stderr, "       add --overwrite to update the existing one")
		}
		return 2
	}
	fmt.Printf("imported scenario %s into %s\n", res.Scenario, *app)
	for _, c := range res.Created {
		fmt.Println("  created", c)
	}
	for _, k := range res.Kept {
		fmt.Println("  kept existing", k)
	}
	for _, m := range res.Missing {
		fmt.Println("  still needed:", m)
	}
	return 0
}

func runDemo(args []string) {
	fs := flag.NewFlagSet("demo", flag.ExitOnError)
	httpAddr := fs.String("http", "127.0.0.1:8089", "orders REST API address")
	grpcAddr := fs.String("grpc", "127.0.0.1:8090", "gRPC address")
	mtlsAddr := fs.String("mtls", "127.0.0.1:8091", "mTLS HTTPS address (empty to disable)")
	grpcTLSAddr := fs.String("grpc-mtls", "127.0.0.1:8092", "mTLS gRPC address (empty to disable)")
	ws := fs.String("workspace", "", "write demo client certificates into this workspace (global/certs)")
	_ = fs.Parse(args)
	var certs []string
	if *ws != "" {
		// Certificate files are global: one copy serves every application.
		certs = append(certs, openStore(*ws).CertsDir())
	}
	die(demo.Run(*httpAddr, *grpcAddr, *mtlsAddr, *grpcTLSAddr, certs))
}
