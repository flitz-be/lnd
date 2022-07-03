package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/jessevdk/go-flags"
)

const (
	ExitCodeSuccess      int = 0
	ExitCodeTargetExists int = 128
	ExitCodeInputMissing int = 129
	ExitCodeFailure      int = 255

	errTargetExists = "target exists error"
	errInputMissing = "input missing error"
)

var (
	log logger = noopLogger
)

type globalOptions struct {
	ErrorOnExisting bool `long:"error-on-existing" short:"e" description:"Exit with code EXIT_CODE_TARGET_EXISTS (128) instead of 0 if the result of an action is already present"`
	Verbose         bool `long:"verbose" short:"v" description:"Turn on logging to stderr"`
}

func main() {
	globalOpts := &globalOptions{}

	// Setup a very minimal logging to stderr if verbose mode is on. To get
	// just the global options, we do a pre-parsing without any commands
	// registered yet. We ignore any errors as that'll be handled later.
	_, _ = flags.NewParser(globalOpts, flags.IgnoreUnknown).Parse()
	if globalOpts.Verbose {
		log = stderrLogger
	}

	parser := flags.NewParser(
		globalOpts, flags.HelpFlag|flags.PassDoubleDash,
	)
	if err := registerCommands(parser); err != nil {
		stderrLogger("Command parser error: %v", err)
		os.Exit(ExitCodeFailure)
	}

	if _, err := parser.Parse(); err != nil {
		flagErr, isFlagErr := err.(*flags.Error)
		switch {
		case isFlagErr:
			if flagErr.Type != flags.ErrHelp {
				// Print error if not due to help request.
				stderrLogger("Config error: %v", err)
				os.Exit(ExitCodeFailure)
			} else {
				// Help was requested, print without any log
				// prefix and exit normally.
				_, _ = fmt.Fprintln(os.Stderr, flagErr.Message)
				os.Exit(ExitCodeSuccess)
			}

		// Ugh, can't use errors.Is() here because the flag parser does
		// not wrap the returned errors properly.
		case strings.Contains(err.Error(), errTargetExists):
			// Respect the user's choice of verbose/non-verbose
			// logging here. The default is quietly aborting if the
			// target already exists.
			if globalOpts.ErrorOnExisting {
				log("Failing on state error: %v", err)
				os.Exit(ExitCodeTargetExists)
			}

			log("Ignoring non-fatal error: %v", err)
			os.Exit(ExitCodeSuccess)

		// Ugh, can't use errors.Is() here because the flag parser does
		// not wrap the returned errors properly.
		case strings.Contains(err.Error(), errInputMissing):
			stderrLogger("Input error: %v", err)
			os.Exit(ExitCodeInputMissing)

		default:
			stderrLogger("Runtime error: %v", err)
			os.Exit(ExitCodeFailure)
		}
	}

	os.Exit(ExitCodeSuccess)
}

type subCommand interface {
	Register(parser *flags.Parser) error
}

func registerCommands(parser *flags.Parser) error {
	commands := []subCommand{
		newMigrateDBCommand(),
	}

	for _, command := range commands {
		if err := command.Register(parser); err != nil {
			return err
		}
	}

	return nil
}