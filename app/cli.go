// cli.go — dexel's command dispatcher (dev_docs/production-runtime/
// ARCHITECTURE.md FORK A and Decision 3, MIGRATION_PLAN.md §PR-3).
//
// One binary is simultaneously the CLI, the runtime, the web UI and (from
// PR-7) the updater (ARCHITECTURE.md Decision 1), so the very first thing
// that happens in this process is deciding WHICH of those it is being
// asked to be. That decision is FORK A's dispatch table, and it is
// factored into `classify` — a pure function over argv — precisely so it
// can be unit-tested without starting a server, a browser, or a
// subprocess (cli_test.go).
package main

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// dispatchKind is what an argv shape MEANS, per FORK A's table:
//
//	| argv shape                     | Meaning                          |
//	| dexel                          | start-if-needed, then open       |
//	| dexel <known-subcommand> [...] | that subcommand                  |
//	| dexel -addr ...                | LEGACY foreground runtime        |
//	| dexel <unknown-word>           | usage, exit 2                    |
//
// The third row is the compatibility rule the whole PR turns on. Every
// invocation that exists today — .github/workflows/desktop.yml's
// `./dexel-server -addr 127.0.0.1:0 -provider fake`, the Tauri sidecar's
// `-addr 127.0.0.1:0`, README's `go run . -fake-script ...` — begins with
// a flag, so all of them keep meaning EXACTLY what they meant before this
// file existed: today's foreground server, today's defaults, today's
// stdout. The only invocation whose meaning changes is bare `dexel` /
// bare `go run .`, which is FORK A's accepted, owner-chosen cost.
type dispatchKind int

const (
	// dispatchBare is `dexel` with no arguments at all: start-if-needed,
	// then open (FORK A's recommended default, "the product behaviour
	// wins").
	dispatchBare dispatchKind = iota
	// dispatchLegacy is `dexel -flag ...`: the pre-PR-3 foreground
	// runtime, byte-for-byte. Recognised by "the first argument starts
	// with '-'", which is a rule about SHAPE rather than a list of
	// flags — so a flag added to the server tomorrow needs no change here
	// and cannot accidentally fall through to the unknown-word branch.
	dispatchLegacy
	// dispatchSubcommand is `dexel <known word> [args]`.
	dispatchSubcommand
	// dispatchUnknown is `dexel <unknown word>`: usage on stderr, exit 2.
	// Deliberately NOT "fall through to the server": a typo like
	// `dexel statsu` silently starting a background runtime is the exact
	// class of surprise ADR 0010's honesty posture rejects.
	dispatchUnknown
)

// decision is classify's answer: what to do, under what name, with which
// remaining arguments.
type decision struct {
	Kind dispatchKind
	Name string   // the subcommand word, or "" for bare/legacy
	Args []string // arguments AFTER the subcommand word (all of argv for legacy)
}

// subcommand is one word of the surface: what `dexel help` says about it,
// and what running it does. Keeping the help text and the implementation
// in ONE table is what makes it structurally impossible for a command to
// exist while being undocumented, or to be documented while unwired —
// classify's "is this word known" and dispatch's "what do I run" read the
// same entry.
type subcommand struct {
	help string
	run  func(args []string) int
}

// subcommands is that table.
//
// ARCHITECTURE.md Decision 3 also specifies `autostart`, `update` and
// `uninstall`. They are deliberately ABSENT until the PRs that own them
// land (PR-6, PR-7): a word that is listed but does nothing is worse than
// a word that honestly reports "unknown command", which is why
// MIGRATION_PLAN.md's exit criteria are per-PR in the first place.
// `pause`/`resume` join the table with PR-5, which is the PR that gives
// them something real to flip (MIGRATION_PLAN.md §PR-5).
var subcommands = map[string]subcommand{
	"start":   {"start the background runtime (detached) and print its URL", cmdStart},
	"stop":    {"stop the background runtime; it saves on the way out", cmdStop},
	"restart": {"stop, wait for exit, then start", cmdRestart},
	"status":  {"is a runtime running? pid, port, url, version, paused [--json]", cmdStatus},
	"pause":   {"stop observing activity (the provider is stopped; nothing accrues)", cmdPause},
	"resume":  {"start observing again, from a clean slate", cmdResume},
	"open":    {"start if needed, then open the UI (desktop app, else browser)", cmdOpen},
	"logs":    {"the runtime log [-n N] [-f] [--path] [--truncate]", cmdLogs},
	"serve":   {"run the server in the FOREGROUND (the developer path; all of today's flags)", func(args []string) int { runServe(modeServe, args); return 0 }},
	"runtime": {"the detached runtime's own entry point — `start` execs this", func(args []string) int { runServe(modeRuntime, args); return 0 }},
	"version": {"print version, commit and os/arch", func([]string) int { fmt.Println(versionLine()); return 0 }},
}

// init registers `help` separately, for a mechanical reason worth stating
// rather than hiding: help's implementation calls usage, usage reads
// subcommands, and a package-level initialiser that (even transitively)
// reads the variable it is initialising is an initialisation cycle the Go
// compiler rejects. Registering it in init(), which runs after all
// package-level variables are initialised, breaks that cycle without
// splitting the table in two.
func init() {
	subcommands["help"] = subcommand{
		help: "print this help",
		run:  func([]string) int { usage(os.Stdout); return 0 },
	}
}

// classify implements FORK A's table over the arguments AFTER the program
// name (i.e. os.Args[1:]). Pure: no filesystem, no environment, no
// process spawning — see this file's doc comment.
func classify(args []string) decision {
	if len(args) == 0 {
		return decision{Kind: dispatchBare}
	}
	first := args[0]
	// SHAPE, not membership: anything beginning with "-" is the legacy
	// foreground runtime, including "-h"/"--help", which therefore keep
	// printing the SERVER's flag usage exactly as they do today rather
	// than being quietly re-pointed at the new CLI help.
	if strings.HasPrefix(first, "-") {
		return decision{Kind: dispatchLegacy, Args: args}
	}
	if _, ok := subcommands[first]; ok {
		return decision{Kind: dispatchSubcommand, Name: first, Args: args[1:]}
	}
	return decision{Kind: dispatchUnknown, Name: first, Args: args[1:]}
}

// usage prints the command surface. Written to the caller's writer so
// `dexel help` can go to stdout (it is what the user asked for) while an
// unknown word goes to stderr (it is an error report).
func usage(w io.Writer) {
	fmt.Fprintf(w, "dexel — your developer companion\n\nUsage:\n  dexel                 start the runtime if needed, then open the UI\n  dexel <command> [flags]\n  dexel -addr ... [...] run the server in the foreground (legacy shape, unchanged)\n\nCommands:\n")
	names := make([]string, 0, len(subcommands))
	for name := range subcommands {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(w, "  %-9s %s\n", name, subcommands[name].help)
	}
	fmt.Fprintf(w, "\nState and logs live under DEXEL_HOME, or the platform default;\n`dexel status` prints both paths.\n")
}

// main is the ~20-line dispatcher ARCHITECTURE.md Decision 2 asks for.
// Every real behaviour lives elsewhere: today's server body is
// runServe (main.go, unchanged logic), the lifecycle verbs are
// cmd_lifecycle.go.
func main() {
	os.Exit(dispatch(classify(os.Args[1:])))
}

// dispatch executes one classified decision and returns the process exit
// code. Split from classify so the mapping argv→meaning is testable
// without executing anything, and split from main so a test can assert an
// exit code instead of killing the test binary.
func dispatch(d decision) int {
	switch d.Kind {
	case dispatchBare:
		// FORK A: bare `dexel` is start-if-needed + open — which is
		// exactly what `dexel open` is specified to do (Decision 3:
		// "ensure running, then show the window"), so it is the same
		// function rather than a second implementation that could drift.
		return cmdOpen(nil)

	case dispatchLegacy:
		// The pre-PR-3 path, deliberately reached with the ENTIRE argv
		// and modeLegacy's today-identical defaults. runServe exits the
		// process itself on failure via log.Fatalf, exactly as today's
		// main() did.
		runServe(modeLegacy, d.Args)
		return 0

	case dispatchSubcommand:
		return subcommands[d.Name].run(d.Args)

	default: // dispatchUnknown
		fmt.Fprintf(os.Stderr, "dexel: unknown command %q\n\n", d.Name)
		usage(os.Stderr)
		return 2
	}
}
