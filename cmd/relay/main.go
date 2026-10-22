// Command relay is the Relay binary: the stage wrapper for GitHub Actions
// (`relay stage <name>`), and in later milestones the bot and the setup
// commands.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"

	"github.com/masteine/relay/internal/bot/core"
	"github.com/masteine/relay/internal/bot/telegram"
	"github.com/masteine/relay/internal/config"
	"github.com/masteine/relay/internal/gh"
	"github.com/masteine/relay/internal/protocol"
	"github.com/masteine/relay/internal/setup"
	"github.com/masteine/relay/internal/stage"
)

// version is set by goreleaser through -ldflags; `go install …@v0.1.0`
// leaves it "dev", and buildVersion takes the module version instead.
var version = buildVersion("dev", debug.ReadBuildInfo)

// releaseRe is a tagged module version: no pseudo-version timestamp and no
// +dirty, so a local or @main build stays "dev" and `relay doctor` does not
// compare it with the plugin.
var releaseRe = regexp.MustCompile(`^v\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$`)
var pseudoRe = regexp.MustCompile(`\d{14}-[0-9a-f]{12}$`)

func buildVersion(v string, read func() (*debug.BuildInfo, bool)) string {
	if v != "dev" {
		return v
	}
	bi, ok := read()
	if !ok || !releaseRe.MatchString(bi.Main.Version) || pseudoRe.MatchString(bi.Main.Version) {
		return v
	}
	return strings.TrimPrefix(bi.Main.Version, "v")
}

const usage = `relay — a message in Telegram becomes a GitHub Issue, a plan, code and a release.

Usage:
  relay setup [--marketplace owner/repo] [--no-token]   once per Mac: checks, plugin, token, bot service
  relay add <alias> <owner/repo>                          per project: bot alias + self-hosted runner
  relay doctor [--repo owner/repo [--check-issue N]]      what is wrong, if anything
  relay bot [--config ~/.config/relay/config.yml]         the bot (normally run by launchd)
  relay stage <route|discuss|ticket|triage|plan|approve|report-failure> [--issue N] [--deadline 13m]
  relay version

Environment (set by GitHub Actions): GITHUB_REPOSITORY, GITHUB_WORKSPACE,
GITHUB_TOKEN, GITHUB_RUN_ID, GITHUB_RUN_ATTEMPT, RELAY_PROTOCOL; for route:
GITHUB_EVENT_NAME, GITHUB_EVENT_PATH, GITHUB_OUTPUT; for report-failure:
RELAY_NEEDS. Optional: RELAY_PLUGIN_DIR (use a plugin checkout),
RELAY_CLAUDE_BIN.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "version", "--version", "-v":
		fmt.Fprintf(stdout, "relay %s (protocol v%d)\n", version, protocol.Version)
		return 0
	case "stage":
		err := stageCmd(args[1:], stderr)
		switch {
		case err == nil:
			return 0
		case errors.Is(err, flag.ErrHelp):
			return 0
		case errors.Is(err, stage.ErrSkip):
			fmt.Fprintf(stderr, "relay: %v\n", err)
			return 0
		case errors.Is(err, stage.ErrStage):
			return 1
		default:
			fmt.Fprintf(stderr, "relay: %v\n", err)
			return 1
		}
	case "setup", "add", "doctor":
		code, err := setupCmd(args[0], args[1:], stdout, stderr)
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		if err != nil {
			fmt.Fprintf(stderr, "relay: %v\n", err)
			if code == 0 {
				code = 1
			}
		}
		return code
	case "bot":
		err := botCmd(args[1:], stderr)
		switch {
		case err == nil, errors.Is(err, context.Canceled), errors.Is(err, flag.ErrHelp):
			return 0
		default:
			fmt.Fprintf(stderr, "relay: %v\n", err)
			return 1
		}
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	}
	fmt.Fprintf(stderr, "relay: unknown command %q\n\n%s", args[0], usage)
	return 2
}

// setupCmd dispatches setup, add and doctor. It returns the exit code.
func setupCmd(name string, args []string, stdout, stderr io.Writer) (int, error) {
	fs := flag.NewFlagSet("relay "+name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	marketplace := fs.String("marketplace", setup.DefaultMarketplace, "GitHub repository that hosts the plugin (setup)")
	noToken := fs.Bool("no-token", false, "keep the stored Telegram token; only rewrite the config and the service (setup)")
	issue := fs.Int("check-issue", 0, "Issue to inspect (doctor)")
	repoName := fs.String("repo", "", "owner/repo of --check-issue (doctor)")
	if err := fs.Parse(args); err != nil {
		return 2, err
	}
	sys := setup.Default()
	sys.Out = stdout
	sys.Version = version
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch name {
	case "setup":
		o := setup.Options{Marketplace: *marketplace, Capture: setup.CaptureTelegram, Confirm: setup.ConfirmTerminal(stdout), CheckToken: setup.CheckTokenTelegram}
		if !*noToken {
			o.AskToken = setup.AskTokenTerminal(stdout)
		}
		if err := sys.Setup(ctx, o); err != nil {
			return 1, err
		}
		return 0, nil
	case "add":
		if fs.NArg() != 2 {
			return 2, errors.New("usage: relay add <alias> <owner/repo>")
		}
		repo, err := gh.ParseRepo(fs.Arg(1))
		if err != nil {
			return 2, err
		}
		if err := sys.Add(ctx, fs.Arg(0), repo); err != nil {
			return 1, err
		}
		return 0, nil
	default: // doctor
		var client gh.Client
		if *issue > 0 && *repoName == "" {
			return 2, errors.New("--check-issue needs --repo owner/repo")
		}
		if *repoName != "" {
			repo, err := gh.ParseRepo(*repoName)
			if err != nil {
				return 2, err
			}
			token, err := gh.Token()
			if err != nil {
				return 1, err
			}
			if client, err = gh.New(repo, token); err != nil {
				return 1, err
			}
		}
		if sys.Doctor(ctx, client, *issue) {
			return 0, nil
		}
		return 1, nil
	}
}

// botCmd runs the bot until SIGINT or SIGTERM: Telegram in, GitHub out,
// polling both, no inbound port.
func botCmd(args []string, stderr io.Writer) error {
	fs := flag.NewFlagSet("relay bot", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", core.DefaultConfigPath(), "bot configuration file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := core.LoadConfig(*cfgPath)
	if err != nil {
		return err
	}
	token, err := telegram.Token(cfg.Telegram.Keychain, cfg.Telegram.TokenFile)
	if err != nil {
		return err
	}
	ghToken, err := gh.Token()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	tg, err := telegram.New(token)
	if err != nil {
		return err
	}
	me, err := tg.Me(ctx)
	if err != nil {
		return fmt.Errorf("telegram: the token does not work: %w", err)
	}
	fmt.Fprintf(stderr, "relay bot: @%s, config %s\n", me, *cfgPath)
	c := core.New(cfg, tg, func(repo gh.Repo) (gh.Client, error) { return gh.New(repo, ghToken) })
	c.Log = stderr
	return c.Run(ctx)
}

func stageCmd(args []string, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("stage name required")
	}
	name := protocol.Stage(args[0])
	fs := flag.NewFlagSet("relay stage "+args[0], flag.ContinueOnError)
	fs.SetOutput(stderr)
	issue := fs.Int("issue", 0, "Issue number (not needed for route)")
	deadline := fs.Duration("deadline", 0, "stop Claude after this long and report the stage as failed")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	if v := os.Getenv("RELAY_PROTOCOL"); v != strconv.Itoa(protocol.Version) {
		if v == "" {
			v = "?"
		}
		return fmt.Errorf("workflow is protocol v%s but this relay speaks v%d: update the relay binary (see README) or regenerate the workflow with /relay:init", v, protocol.Version)
	}
	dir := os.Getenv("GITHUB_WORKSPACE")
	if dir == "" {
		var err error
		if dir, err = os.Getwd(); err != nil {
			return err
		}
	}
	repoName := os.Getenv("GITHUB_REPOSITORY")
	if repoName == "" {
		return errors.New("GITHUB_REPOSITORY is not set; relay stage runs inside GitHub Actions")
	}
	repo, err := gh.ParseRepo(repoName)
	if err != nil {
		return err
	}
	token, err := gh.Token()
	if err != nil {
		return err
	}
	client, err := gh.New(repo, token)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// route and report-failure run without a checkout and read relay.yml
	// through the API; the stages that run Claude read it from the
	// workspace they are about to work in.
	var cfg config.Project
	if name == "route" || name == "report-failure" || name == protocol.StageApprove {
		raw, err := client.FileContent(ctx, config.FileName)
		if err != nil {
			return err
		}
		if cfg, err = config.Parse([]byte(raw)); err != nil {
			return err
		}
	} else if cfg, err = config.Load(filepath.Join(dir, config.FileName)); err != nil {
		return err
	}
	runID := os.Getenv("GITHUB_RUN_ID")
	if a := os.Getenv("GITHUB_RUN_ATTEMPT"); runID != "" && a != "" {
		runID += "." + a
	}
	r := &stage.Runner{
		Repo:   repo,
		GH:     client,
		Config: cfg,
		Dir:    dir,
		RunID:  runID,
		Log:    stderr,
		Claude: &stage.Claude{Dir: dir, Bin: os.Getenv("RELAY_CLAUDE_BIN"), PluginDir: os.Getenv("RELAY_PLUGIN_DIR")},
	}

	needIssue := func() error {
		if *issue <= 0 {
			return fmt.Errorf("--issue is required for %s", name)
		}
		return nil
	}
	runsClaude := name == protocol.StageDiscuss || name == protocol.StageTicket || name == protocol.StageTriage || name == protocol.StagePlan
	if runsClaude {
		if err := needIssue(); err != nil {
			return err
		}
		if err := r.Claude.Preflight(ctx); err != nil {
			return err
		}
	}

	switch name {
	case "route":
		payload, err := os.ReadFile(os.Getenv("GITHUB_EVENT_PATH"))
		if err != nil {
			return fmt.Errorf("GITHUB_EVENT_PATH: %w", err)
		}
		var outputs io.Writer
		if p := os.Getenv("GITHUB_OUTPUT"); p != "" {
			f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
			if err != nil {
				return err
			}
			defer f.Close()
			outputs = f
		} else {
			outputs = os.Stdout
		}
		_, err = r.Route(ctx, os.Getenv("GITHUB_EVENT_NAME"), payload, outputs)
		return err
	case protocol.StageDiscuss:
		return r.Discuss(ctx, *issue, *deadline)
	case protocol.StageTicket:
		return r.Ticket(ctx, *issue, *deadline)
	case protocol.StageTriage:
		return r.Triage(ctx, *issue, *deadline)
	case protocol.StagePlan:
		return r.Plan(ctx, *issue, *deadline)
	case protocol.StageApprove:
		if err := needIssue(); err != nil {
			return err
		}
		return r.Approve(ctx, *issue)
	case "report-failure":
		if err := needIssue(); err != nil {
			return err
		}
		return r.ReportFailure(ctx, *issue, []byte(os.Getenv("RELAY_NEEDS")))
	}
	return fmt.Errorf("unknown stage %q", name)
}
