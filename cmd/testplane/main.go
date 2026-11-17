// Command testplane runs the test workspace: as a web server with the UI,
// or headless from a terminal or CI pipeline.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

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
  testplane serve     --workspace DIR [--addr :8080]          web UI + REST API
  testplane run       --workspace DIR --app ID [options]      run tests headless (CI/CD)
  testplane validate  --workspace DIR [--app ID]              check YAML and auth assets
  testplane list      --workspace DIR                         list applications and scenarios
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
	case "validate":
		os.Exit(validate(args))
	case "list":
		list(args)
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
	_ = fs.Parse(args)
	if err := os.MkdirAll(filepath.Join(*ws, "applications"), 0o755); err != nil {
		die(err)
	}
	srv := api.New(store.New(*ws), web.FS())
	fmt.Printf("testplane %s\nworkspace %s\nlistening on http://%s\n", version, *ws, *addr)
	die(http.ListenAndServe(*addr, srv.Handler()))
}

func run(args []string) int {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	ws := workspaceFlag(fs)
	app := fs.String("app", "", "application id (required)")
	env := fs.String("env", "", "environment name (default: the application's default)")
	kind := fs.String("kind", "", "positive = skip negative cases, negative = skip positive variants")
	junit := fs.String("junit", "", "write a JUnit XML report to this file")
	jsonOut := fs.String("json", "", "write the full JSON result to this file")
	save := fs.Bool("save", false, "also store the run in the workspace so the UI lists it")
	quiet := fs.Bool("quiet", false, "print only the summary")
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
	st := store.New(*ws)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	last := ""
	opts := engine.Options{App: *app, Env: *env, Scenarios: csv(scenarios), Tags: csv(tags), Kind: *kind, Vars: overrides}
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

func validate(args []string) int {
	fs := flag.NewFlagSet("validate", flag.ExitOnError)
	ws := workspaceFlag(fs)
	only := fs.String("app", "", "validate one application (default: all)")
	_ = fs.Parse(args)
	st := store.New(*ws)
	apps, err := st.ListApps()
	if err != nil {
		die(err)
	}
	problems, checked := 0, 0
	report := func(where, msg string) {
		problems++
		fmt.Printf("  PROBLEM %s: %s\n", where, msg)
	}
	for _, a := range apps {
		if *only != "" && a.ID != *only {
			continue
		}
		checked++
		fmt.Printf("%s\n", a.ID)
		profiles, err := st.ListAuth(a.ID)
		if err != nil {
			die(err)
		}
		known := map[string]bool{model.AuthNone: true}
		for _, p := range profiles {
			known[p.ID] = true
			where := "auth/" + p.ID
			if strings.HasPrefix(p.Description, "INVALID: ") {
				report(where, strings.TrimPrefix(p.Description, "INVALID: "))
				continue
			}
			for _, e := range p.Validate() {
				report(where, e)
			}
			for _, c := range auth.CertChecks(&p, st.AssetDir(a.ID, "certs")) {
				if c.Status != "ok" && (c.Required || c.Status != "not_set") {
					report(where, fmt.Sprintf("%s (%s): %s %s", c.Label, c.Field, c.Status, c.Detail))
				}
			}
		}
		if a.DefaultAuth != "" && !known[a.DefaultAuth] {
			report("application", "defaultAuth "+a.DefaultAuth+" is not a known auth profile")
		}
		if a.DefaultEnvironment != "" {
			if _, ok := a.Environments[a.DefaultEnvironment]; !ok {
				report("application", "defaultEnvironment "+a.DefaultEnvironment+" is not defined")
			}
		}
		scs, err := st.ListScenarios(a.ID)
		if err != nil {
			die(err)
		}
		tests := 0
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
			for _, t := range sc.Tests {
				tests += 1 + len(t.Variants)
				refs = append(refs, t.Auth)
				for _, v := range t.Variants {
					refs = append(refs, v.Auth)
				}
			}
			for _, r := range refs {
				if r != "" && !known[r] {
					report(where, "auth profile "+r+" does not exist")
					known[r] = true
				}
			}
		}
		fmt.Printf("  %d scenario(s), %d test(s), %d auth profile(s)\n", len(scs), tests, len(profiles))
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

func list(args []string) {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	ws := workspaceFlag(fs)
	_ = fs.Parse(args)
	st := store.New(*ws)
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

func runDemo(args []string) {
	fs := flag.NewFlagSet("demo", flag.ExitOnError)
	httpAddr := fs.String("http", "127.0.0.1:8089", "orders REST API address")
	grpcAddr := fs.String("grpc", "127.0.0.1:8090", "gRPC address")
	mtlsAddr := fs.String("mtls", "127.0.0.1:8091", "mTLS HTTPS address (empty to disable)")
	ws := fs.String("workspace", "", "write demo client certificates into this workspace's orders-demo application")
	_ = fs.Parse(args)
	certs := ""
	if *ws != "" {
		certs = store.New(*ws).AssetDir("orders-demo", "certs")
	}
	die(demo.Run(*httpAddr, *grpcAddr, *mtlsAddr, certs))
}
