// cmd_autostart.go — `dexel autostart enable|disable|status`
// (dev_docs/production-runtime/MIGRATION_PLAN.md §PR-6,
// dev_docs/production-runtime/PLATFORM_NOTES.md §3).
//
// One CLI verb, three OS mechanisms, ONE non-negotiable rule
// (PLATFORM_NOTES.md §3, ARCHITECTURE.md's consent posture): autostart
// is never enabled by anything other than an explicit `dexel autostart
// enable` run by the user. This file is the ONLY caller of
// autostart.Enable in the whole binary — not the installer (PR-8's
// install.sh explicitly leaves it off), not `dexel start`, not
// first-run. `enable` is idempotent (a second run yields the same one
// entry, never two, all the way down in app/internal/autostart); the
// binary path baked into whichever mechanism gets written starts from
// cliEnv.self (os.Executable(), resolved once in cliEnvOrReport) — never
// os.Args[0] — because it must point at the INSTALLED binary autostart
// will invoke at the next login, not however this particular invocation
// happened to be spelled.
//
// On macOS the package may substitute an installed /Applications/Dexel.app
// bundle's own executable for cliEnv.self, so that System Settings →
// Login Items & Extensions can attribute the background item to a bundle
// and show its icon and name instead of a generic `exec`
// (app/internal/autostart/autostart.go's launchdProgram explains the
// mechanism and the evidence). Because of that substitution `enable` no
// longer prints env.self — it prints autostart.Result.Program, the path
// that was really written — and `--bare` opts out.
//
// Dispatches the same way cmdPause/cmdResume fan a single lifecycle verb
// out to sub-behaviour (cmd_lifecycle.go's cmdSetPaused): one table entry
// in cli.go's subcommands map, one word of argv consumed here to pick
// enable/disable/status.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/jawwadzafar/dexel/app/internal/autostart"
	"github.com/jawwadzafar/dexel/app/internal/store"
)

func cmdAutostart(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "dexel: autostart requires a sub-command: enable | disable | status")
		return 2
	}
	verb, rest := args[0], args[1:]
	switch verb {
	case "enable":
		return cmdAutostartEnable(rest)
	case "disable":
		return cmdAutostartDisable(rest)
	case "status":
		return cmdAutostartStatus(rest)
	default:
		fmt.Fprintf(os.Stderr, "dexel: autostart: unknown sub-command %q (want enable | disable | status)\n", verb)
		return 2
	}
}

// cmdAutostartEnable installs the platform mechanism (launchd /
// systemd-user-or-xdg-autostart / HKCU Run, PLATFORM_NOTES.md §3) and
// records which one in config.json. --linger is never implied: a
// companion that tracks *your keyboard* running with nobody logged in
// is a surprise, not a sensible default (PLATFORM_NOTES.md §3.2).
func cmdAutostartEnable(args []string) int {
	fs := flag.NewFlagSet("dexel autostart enable", flag.ExitOnError)
	linger := fs.Bool("linger", false, "also run `loginctl enable-linger` (Linux/systemd-user only) so the runtime survives with nobody logged in -- NOT implied by plain enable")
	// No backticks in this usage string: package flag treats a backquoted
	// word as the name of the flag's VALUE, which is nonsense for a bool --
	// `dexel autostart enable -h` rendered "-bare dexel update", as if
	// --bare took an argument.
	bare := fs.Bool("bare", false, "macOS only: point the launchd plist at this binary instead of an installed Dexel.app's executable -- keeps 'dexel update' in charge of what runs at login, at the cost of a generic icon in System Settings")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	env, code := cliEnvOrReport()
	if env == nil {
		return code
	}

	res, err := autostart.Enable(env.self, env.logPath, autostart.Options{BareExecutable: *bare})
	if err != nil {
		fmt.Fprintf(env.errOut, "dexel: autostart enable: %v\n", err)
		return 1
	}

	if err := recordAutostartMechanism(res.Mechanism); err != nil {
		// The OS-level mechanism is already in place; failing to record
		// it in config.json is reported but never reverted -- `disable`
		// probes the OS directly on Linux and `status` always asks the
		// OS (autostart.Query), so a stale/missing config.json entry is
		// cosmetic drift, never a correctness problem.
		fmt.Fprintf(env.errOut, "dexel: autostart enabled (%s) but failed to record it in config.json: %v\n", res.Mechanism, err)
	}

	// Print res.Program, NOT env.self: on macOS they can differ, and
	// printing the path we asked for rather than the one that was
	// written would be exactly the kind of "looks right, isn't" output
	// this project refuses to ship.
	fmt.Fprintf(env.out, "dexel: autostart ENABLED via %s (%s)\n", res.Mechanism, res.Program)
	if res.Note != "" {
		fmt.Fprintf(env.out, "  %s\n", res.Note)
	}

	if *linger {
		if err := autostart.EnableLinger(); err != nil {
			fmt.Fprintf(env.errOut, "dexel: --linger: %v\n", err)
			return 1
		}
		fmt.Fprintln(env.out, "dexel: linger enabled -- the runtime will also run with nobody logged in")
	}
	return 0
}

// cmdAutostartDisable removes autostart. On Linux, autostart.Disable
// probes BOTH systemd-user and xdg-autostart regardless of what
// config.json records (PLATFORM_NOTES.md §3.2), so a user who switched
// distros is never left with an orphaned entry.
func cmdAutostartDisable(args []string) int {
	fs := flag.NewFlagSet("dexel autostart disable", flag.ExitOnError)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	env, code := cliEnvOrReport()
	if env == nil {
		return code
	}
	if err := autostart.Disable(); err != nil {
		fmt.Fprintf(env.errOut, "dexel: autostart disable: %v\n", err)
		return 1
	}
	if err := recordAutostartMechanism(autostart.MechanismNone); err != nil {
		fmt.Fprintf(env.errOut, "dexel: autostart disabled but failed to clear config.json: %v\n", err)
	}
	fmt.Fprintln(env.out, "dexel: autostart DISABLED")
	return 0
}

// cmdAutostartStatus always asks the OS (autostart.Query), and reports
// -- honestly, never silently reconciled -- if config.json's recorded
// mechanism disagrees, the same "staleness is resolved by asking"
// posture cmdStatus already applies to runtime.json vs a live
// lifecycle round-trip (cmd_lifecycle.go).
func cmdAutostartStatus(args []string) int {
	fs := flag.NewFlagSet("dexel autostart status", flag.ExitOnError)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	env, code := cliEnvOrReport()
	if env == nil {
		return code
	}
	st, err := autostart.Query()
	if err != nil {
		fmt.Fprintf(env.errOut, "dexel: autostart status: %v\n", err)
		return 1
	}

	if st.Mechanism == autostart.MechanismNone {
		fmt.Fprintln(env.out, "dexel: autostart is OFF")
	} else {
		fmt.Fprintf(env.out, "dexel: autostart is ON via %s (active=%v)\n", st.Mechanism, st.Active)
	}
	if st.Detail != "" {
		fmt.Fprintf(env.out, "  %s\n", st.Detail)
	}

	cfgPath, cfgErr := store.ConfigPath()
	if cfgErr == nil {
		if cfg, err := store.LoadConfig(cfgPath); err == nil {
			if recorded := autostart.Mechanism(cfg.Autostart); recorded != st.Mechanism {
				fmt.Fprintf(env.out, "  (config.json records %q, which disagrees with what the OS reports -- the OS is authoritative)\n", string(recorded))
			}
		}
	}
	return 0
}

// recordAutostartMechanism writes mechanism into config.json's
// Autostart field, load-modify-save so every other field already there
// (Name, SessionNames) survives untouched -- SEC-1 design's config.json
// contract, MIGRATION_PLAN.md §PR-6: "`enable` records the mechanism in
// config.json".
func recordAutostartMechanism(mechanism autostart.Mechanism) error {
	cfgPath, err := store.ConfigPath()
	if err != nil {
		return err
	}
	cfg, err := store.LoadConfig(cfgPath)
	if err != nil {
		return err
	}
	cfg.Autostart = string(mechanism)
	return store.SaveConfig(cfgPath, cfg)
}
