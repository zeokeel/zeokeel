package main

// `zeokeel update` — check/download/install a ZeoKeel update from the CLI.
//
// The engine lives in internal/updater (check → download+SHA-256 → stage →
// install-on-restart). This command is the thin, scriptable surface:
//
//	zeokeel update                 # check (default), human output
//	zeokeel update --check --json  # machine-readable check result
//	zeokeel update --download      # check + download into <data dir>/updates
//	zeokeel update --install       # check + download + install now (atomic
//	                               # rename; the new binary runs on next start)
//
// Feed selection: --feed flag, else ZEOKEEL_UPDATE_FEED, else error (no feed is
// configured; the release feed URL is a product decision, not a hardcoded
// default). ZEOKEEL_UPDATE_ALLOW_HTTP=1 is a test/development escape hatch for
// the https-only policy.
//
// Exit codes: 0 command completed (including "up to date"), 1 error, 2 usage.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"

	"github.com/zeokeel/zeokeel/old/internal/app"
	"github.com/zeokeel/zeokeel/old/internal/updater"
)

const updateUsage = `Usage: zeokeel update [action] [options]

Check for, download and install a ZeoKeel update. Builds are unsigned; the
SHA-256 from the release manifest is always verified before anything is staged.

Actions (at most one; default --check):
  --check        Check for an update and report it (exit 0 even when up to date)
  --download     Check and download the update into <data dir>/updates
  --install      Check, download and install now: the running binary is replaced
                 atomically (rename) and the new build runs on the next start

Options:
  --json         Print one JSON object on stdout (diagnostics stay on stderr)
  --feed <url>   Update manifest URL (default: ZEOKEEL_UPDATE_FEED)

Environment:
  ZEOKEEL_UPDATE_FEED        release manifest URL (required unless --feed)
  ZEOKEEL_UPDATE_ALLOW_HTTP  "1" allows http:// feed/asset URLs (tests only)
  ZEOKEEL_DATA_DIR           data directory; staging lives in <dir>/updates

Exit codes: 0 completed (including up to date), 1 error, 2 usage error.
`

// updateAction is the requested CLI action.
type updateAction int

const (
	actionCheck updateAction = iota
	actionDownload
	actionInstall
)

func (a updateAction) String() string {
	switch a {
	case actionDownload:
		return "download"
	case actionInstall:
		return "install"
	default:
		return "check"
	}
}

// updateOptions is the parsed `zeokeel update` command line.
type updateOptions struct {
	help   bool
	json   bool
	feed   string
	action updateAction
}

// updateResult is the --json document. Fields are stable for scripts.
type updateResult struct {
	CurrentVersion string `json:"current_version"`
	Feed           string `json:"feed,omitempty"`
	Action         string `json:"action"`
	Status         string `json:"status"` // up_to_date | update_available | downloaded | installed
	LatestVersion  string `json:"latest_version,omitempty"`
	Notes          string `json:"notes,omitempty"`
	StagedPath     string `json:"staged_path,omitempty"`
	Applied        bool   `json:"applied,omitempty"`
	Error          string `json:"error,omitempty"`
}

func parseUpdateArgs(args []string) (updateOptions, error) {
	var (
		opts     updateOptions
		seen     int
		selected updateAction
	)
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-h" || arg == "--help":
			opts.help = true
		case arg == "--json":
			opts.json = true
		case arg == "--check":
			seen++
			selected = actionCheck
		case arg == "--download":
			seen++
			selected = actionDownload
		case arg == "--install":
			seen++
			selected = actionInstall
		case arg == "--feed":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--feed needs a URL")
			}
			i++
			opts.feed = args[i]
		case strings.HasPrefix(arg, "--feed="):
			opts.feed = strings.TrimPrefix(arg, "--feed=")
		default:
			return opts, fmt.Errorf("unexpected argument %q", arg)
		}
	}
	if opts.help {
		return opts, nil
	}
	if seen > 1 {
		return opts, fmt.Errorf("--check, --download and --install are mutually exclusive")
	}
	opts.action = selected
	return opts, nil
}

// updateReplacer is the platform seam used by --install. Tests swap it for a
// fake; production renames the running executable aside and moves the verified
// staged binary into place (atomic on both Unix and Windows, where a running
// executable can be renamed but not deleted).
var updateReplacer updater.Replacer = exeReplacer{}

// runUpdateCLI implements `zeokeel update` and returns the process exit code.
func runUpdateCLI(args []string, stdout, stderr io.Writer) int {
	opts, err := parseUpdateArgs(args)
	if err != nil {
		fmt.Fprintf(stderr, "zeokeel update: %v\n\n%s", err, updateUsage)
		return 2
	}
	if opts.help {
		fmt.Fprint(stdout, updateUsage)
		return 0
	}

	feed := strings.TrimSpace(opts.feed)
	if feed == "" {
		feed = strings.TrimSpace(os.Getenv("ZEOKEEL_UPDATE_FEED"))
	}
	if feed == "" {
		fmt.Fprintln(stderr, "zeokeel update: no update feed configured (set --feed <url> or ZEOKEEL_UPDATE_FEED)")
		return 1
	}

	cfg := updater.Config{
		CurrentVersion:    version,
		FeedURL:           feed,
		DataDir:           app.DataDir(),
		Replacer:          updateReplacer,
		AllowInsecureHTTP: os.Getenv("ZEOKEEL_UPDATE_ALLOW_HTTP") == "1",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	fail := func(err error) int {
		log.Printf("zeokeel update: %v", err)
		fmt.Fprintf(stderr, "zeokeel update: %v\n", err)
		if opts.json {
			data, _ := json.MarshalIndent(updateResult{
				CurrentVersion: version, Feed: feed, Action: opts.action.String(),
				Status: "error", Error: err.Error(),
			}, "", "  ")
			fmt.Fprintf(stdout, "%s\n", data)
		}
		return 1
	}

	result := updateResult{CurrentVersion: version, Feed: feed, Action: opts.action.String()}
	release, available, err := updater.Check(ctx, cfg)
	if err != nil {
		return fail(err)
	}
	if !available {
		result.Status = "up_to_date"
		return printUpdateResult(opts, stdout, result)
	}
	result.LatestVersion = release.Version
	result.Notes = release.Notes
	if opts.action == actionCheck {
		result.Status = "update_available"
		return printUpdateResult(opts, stdout, result)
	}

	staged, err := updater.Download(ctx, cfg, *release)
	if err != nil {
		return fail(err)
	}
	result.StagedPath = staged.Path
	if opts.action == actionDownload {
		result.Status = "downloaded"
		return printUpdateResult(opts, stdout, result)
	}

	if err := updater.ScheduleInstall(cfg, *staged); err != nil {
		return fail(err)
	}
	applied, err := updater.ApplyPendingInstall(cfg)
	if err != nil {
		// The download is kept for the next start (install-on-restart); report
		// it as an error so scripts do not assume success.
		return fail(err)
	}
	result.Status = "installed"
	result.Applied = applied
	return printUpdateResult(opts, stdout, result)
}

// printUpdateResult renders the result as JSON or human text; always exit 0.
func printUpdateResult(opts updateOptions, stdout io.Writer, result updateResult) int {
	if opts.json {
		data, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "zeokeel update: encode result: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "%s\n", data)
		return 0
	}
	switch result.Status {
	case "up_to_date":
		fmt.Fprintf(stdout, "ZeoKeel %s is up to date (feed: %s)\n", result.CurrentVersion, result.Feed)
	case "update_available":
		fmt.Fprintf(stdout, "update available: %s (current %s)\n", result.LatestVersion, result.CurrentVersion)
		if result.Notes != "" {
			fmt.Fprintf(stdout, "notes: %s\n", result.Notes)
		}
		fmt.Fprintln(stdout, "run `zeokeel update --download` to download, or `zeokeel update --install` to install")
	case "downloaded":
		fmt.Fprintf(stdout, "downloaded %s -> %s\n", result.LatestVersion, result.StagedPath)
		fmt.Fprintln(stdout, "run `zeokeel update --install` to apply it")
	case "installed":
		fmt.Fprintf(stdout, "installed %s; restart ZeoKeel to run the new build\n", result.LatestVersion)
	default:
		fmt.Fprintf(stdout, "%s\n", result.Status)
	}
	return 0
}
