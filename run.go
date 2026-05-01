//go:generate go run ./tools/genprops -out docs/generated/connection-properties.generated.md
//go:generate mise exec -- ptyhelp patch -file README.md -marker spannersh-help -cols 256 -o docs/generated/spannersh-help.txt -- go run . --help

package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"regexp"
	"strings"

	"cloud.google.com/go/spanner/admin/database/apiv1/databasepb"
	"github.com/alecthomas/kong"
	"github.com/hymkor/go-multiline-ny"
	"github.com/nyaosorg/go-readline-ny"
	"github.com/nyaosorg/go-readline-ny/keys"
	"github.com/nyaosorg/go-readline-ny/simplehistory"
)

const (
	promptMain     = "spanner> "
	promptContinue = "      -> "
)

var reExitCommand = regexp.MustCompile(`(?i)^\s*(exit|quit)\s*;?\s*$`)

type cliOpts struct {
	ShowVersion bool   `name:"version" help:"Print version and exit."`
	Project     string `short:"p" name:"project" env:"SPANNER_PROJECT_ID" placeholder:"PROJECT" help:"Google Cloud Project ID. Default: ${env}."`
	Instance    string `short:"i" name:"instance" env:"SPANNER_INSTANCE_ID" placeholder:"INSTANCE" help:"Spanner instance ID. Default: ${env}."`
	Database    string `short:"d" name:"database" env:"SPANNER_DATABASE_ID" placeholder:"DATABASE" help:"Database ID. Default: ${env}."`
	// DSNSuffix is appended to the base projects/.../instances/.../databases/... DSN.
	// It is passed through to go-sql-spanner connection properties after minimal trimming.
	DSNSuffix string `name:"dsn-suffix" placeholder:"PARAMS" help:"Extra go-sql-spanner DSN parameters (snake_case; semicolon-separated). See docs."`
	// Dialect affects only spannersh's client-side parsing and DSN composition.
	// "auto" reads the driver's database_dialect property after connect; explicit PostgreSQL
	// also prepends dialect=POSTGRESQL to the DSN unless --dsn-suffix already set dialect=.
	Dialect string `name:"dialect" default:"auto" placeholder:"DIALECT" help:"Client-side SQL parser dialect: auto, google-standard-sql, or postgresql. PostgreSQL adds dialect=POSTGRESQL to the DSN unless --dsn-suffix already sets dialect=."`
	// Format selects result-set rendering only. EXPLAIN / EXPLAIN ANALYZE plan trees ignore it.
	Format string `name:"format" default:"table" placeholder:"FORMAT" help:"Output format: table, csv, or jsonl. EXPLAIN plan output is always a text plan tree."`
}

type cliExitError struct {
	code int
}

func (e cliExitError) Error() string {
	return fmt.Sprintf("CLI requested exit with status %d", e.code)
}

func run(ctx context.Context) error {
	out := os.Stdout
	errOut := os.Stderr

	opts, err := parseCLIOpts(os.Args[1:], out, errOut)
	if err != nil {
		return err
	}

	if opts.ShowVersion {
		fmt.Fprintf(out, "spannersh %s\n", resolvedVersion())
		return nil
	}
	if err := opts.validateConnectionTarget(); err != nil {
		return err
	}

	cli, err := openCLIApp(ctx, out, errOut, opts)
	if err != nil {
		return err
	}
	defer cli.db.Close()
	return runREPL(ctx, cli, errOut)
}

// replInputComplete is true when the multiline buffer should be submitted on Enter.
// The whole buffer (joined with newlines, trimmed) must end with ';' or be a sole exit/quit command.
// go-sql-spanner accepts multi-statement SQL in QueryContext (multiple result sets); see
// https://github.com/googleapis/go-sql-spanner/releases/tag/v1.22.0
func replInputComplete(lines []string) bool {
	b := strings.TrimSpace(strings.Join(lines, "\n"))
	if b == "" {
		return false
	}
	return strings.HasSuffix(b, ";") || reExitCommand.MatchString(b)
}

// configureREPLEditor wires history, multiline submit rules, keys, and prompts on ed (must not be copied; it holds a mutex).
func configureREPLEditor(ed *multiline.Editor) (*simplehistory.Container, error) {
	hist := simplehistory.New()
	ed.SetHistory(hist)
	ed.SetHistoryCycling(true)
	ed.SubmitOnEnterWhen(func(lines []string, _ int) bool {
		return replInputComplete(lines)
	})
	if err := ed.BindKey(keys.CtrlJ, readline.AnonymousCommand(ed.NewLine)); err != nil {
		return hist, err
	}
	ed.SetPrompt(func(writer io.Writer, i int) (int, error) {
		switch i {
		case 0:
			return writer.Write([]byte(promptMain))
		default:
			return writer.Write([]byte(promptContinue))
		}
	})
	return hist, nil
}

func parseCLIOpts(args []string, out, errOut io.Writer) (cliOpts, error) {
	var opts cliOpts
	var exitCode *int
	parser, err := kong.New(
		&opts,
		kong.Name("spannersh"),
		kong.Description("Interactive shell for Google Cloud Spanner."),
		kong.Writers(out, errOut),
		kong.Exit(func(code int) {
			exitCode = &code
		}),
	)
	if err != nil {
		return opts, err
	}
	if _, err := parser.Parse(args); err != nil {
		return opts, err
	}
	if exitCode != nil {
		return opts, cliExitError{code: *exitCode}
	}
	return opts, nil
}

func (opts cliOpts) validateConnectionTarget() error {
	if opts.Project == "" || opts.Instance == "" || opts.Database == "" {
		return fmt.Errorf("required flags missing: -p/--project, -i/--instance, -d/--database (or set SPANNER_PROJECT_ID, SPANNER_INSTANCE_ID, SPANNER_DATABASE_ID; see --help)")
	}
	return nil
}

func openCLIApp(ctx context.Context, out, errOut io.Writer, opts cliOpts) (*app, error) {
	dialectEnum, autoDialect, err := parseCLIDialect(opts.Dialect)
	if err != nil {
		return nil, err
	}
	db, err := openSpannerDB(opts, dsnDialectForCLI(dialectEnum, autoDialect))
	if err != nil {
		return nil, err
	}
	return &app{
		ctx:     ctx,
		out:     out,
		db:      db,
		format:  outputFormatFromString(opts.Format),
		dialect: resolveEffectiveDialect(ctx, errOut, db, dialectEnum, autoDialect),
	}, nil
}

func dsnDialectForCLI(dialect databasepb.DatabaseDialect, autoDetect bool) databasepb.DatabaseDialect {
	if autoDetect {
		return databasepb.DatabaseDialect_GOOGLE_STANDARD_SQL
	}
	return dialect
}

func openSpannerDB(opts cliOpts, dsnDialect databasepb.DatabaseDialect) (*sql.DB, error) {
	return sql.Open("spanner", composeSpannerDSN(opts.Project, opts.Instance, opts.Database, dsnDialect, opts.DSNSuffix))
}

func resolveEffectiveDialect(ctx context.Context, errOut io.Writer, db *sql.DB, dialect databasepb.DatabaseDialect, autoDetect bool) databasepb.DatabaseDialect {
	if !autoDetect {
		startBackgroundWarmup(ctx, errOut, db)
		return dialect
	}
	d, err := detectDatabaseDialect(ctx, db)
	if err != nil {
		fmt.Fprintf(errOut, "could not read SQL dialect from driver: %v (using google-standard-sql for client-side parsing)\n", err)
		return databasepb.DatabaseDialect_GOOGLE_STANDARD_SQL
	}
	return d
}

// replHandleReadErr maps readline errors to REPL control flow. stop means leave the read loop;
// loopErr is nil on EOF, or the error to return (e.g. context.Canceled).
func replHandleReadErr(err error, errOut io.Writer) (stop bool, loopErr error) {
	switch {
	case errors.Is(err, io.EOF):
		return true, nil
	case errors.Is(err, context.Canceled):
		fmt.Fprintln(errOut, "\ninterrupted")
		return true, err
	default:
		fmt.Fprintf(errOut, "ERROR: %v\n", err)
		return false, nil
	}
}

// execInput runs one user statement and prints driver/query errors to errOut; context.Canceled continues the REPL.
func (a *app) execInput(ctx context.Context, input string, errOut io.Writer) {
	if err := a.executeAndRenderContext(ctx, input); err != nil {
		if errors.Is(err, context.Canceled) {
			fmt.Fprintln(errOut, "interrupted")
			return
		}
		fmt.Fprintf(errOut, "ERROR: %v\n", err)
	}
}

func contextWithInterrupt(parent context.Context, interrupts <-chan os.Signal) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	go func() {
		select {
		case <-interrupts:
			cancel()
		case <-parent.Done():
			cancel()
		case <-done:
		}
	}()
	return ctx, func() {
		close(done)
		cancel()
	}
}

func runREPL(ctx context.Context, cli *app, errOut io.Writer) error {
	var ed multiline.Editor
	hist, err := configureREPLEditor(&ed)
	if err != nil {
		return err
	}
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt)
	defer signal.Stop(interrupts)

	for {
		readCtx, cancelRead := contextWithInterrupt(ctx, interrupts)
		lines, err := ed.Read(readCtx)
		cancelRead()
		if err != nil {
			if stop, loopErr := replHandleReadErr(err, errOut); stop {
				return loopErr
			}
			continue
		}
		if len(lines) == 0 {
			return nil
		}

		input := strings.Join(lines, "\n")
		if reExitCommand.MatchString(input) {
			return nil
		}
		hist.Add(input)
		queryCtx, cancelQuery := contextWithInterrupt(ctx, interrupts)
		cli.execInput(queryCtx, input, errOut)
		cancelQuery()
	}
}
