package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/cgrossde/slackcli/cmd"
	"github.com/cgrossde/slackcli/internal/browser"
	"github.com/cgrossde/slackcli/internal/keychain"
	"github.com/cgrossde/slackcli/internal/output"
	"github.com/cgrossde/slackcli/internal/slack"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		if errors.Is(err, context.Canceled) {
			os.Exit(130)
		}
		os.Exit(1)
	}
}

// run is the testable entry point. stdout receives presenter-formatted output;
// stderr receives progress messages and slog output.
// Any error returned by Cobra (flag errors, unknown commands) is formatted
// through the presenter so the caller always gets a [exit:N | Xms] footer.
// errAlreadyPresented is returned by RunE implementations that have already
// written formatted output through the presenter. run() recognises this and
// exits non-zero without writing a second presenter block.
var errAlreadyPresented = errors.New("already presented")

func run(args []string, stdout, stderr io.Writer) error {
	slog.SetDefault(slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	start := time.Now()
	root := buildRoot(stdout, stderr)
	root.SetArgs(args)
	err := root.Execute()
	if err == nil {
		return nil
	}
	// errAlreadyPresented means a RunE already wrote the formatted output.
	// Exit non-zero without a second presenter block.
	if errors.Is(err, errAlreadyPresented) {
		return err
	}
	// context.Canceled means Ctrl+C — propagate as-is so main() can exit 130.
	if errors.Is(err, context.Canceled) {
		return err
	}
	// All other errors (missing required flags, unknown commands, etc.) go
	// through the presenter so the output is always structured.
	// Include the relevant command's usage so the agent knows what to supply.
	usageStr := ""
	if found, _, findErr := root.Find(args); findErr == nil && found != nil {
		usageStr = found.UsageString()
	}
	output.Print(stdout, stderr, output.Result{
		Stdout:   usageStr,
		Stderr:   err.Error(),
		ExitCode: 1,
		Elapsed:  time.Since(start),
	})
	return nil
}

// buildRoot constructs the full Cobra command tree and returns the root command.
// stdout/stderr are the injected writers so every command's output is testable.
func buildRoot(stdout, stderr io.Writer) *cobra.Command {
	root := &cobra.Command{
		Use:           "slackcli",
		Short:         "Interact with Slack using browser-extracted credentials",
		SilenceUsage:  true, // don't dump usage on every RunE error
		SilenceErrors: true, // we handle error printing ourselves via the presenter
	}
	root.SetOut(stdout)
	root.SetErr(stderr)

	// Help template: Usage+Flags first, then Long description.
	// Cobra's default puts Long first, which buries the flags for an LLM caller.
	root.SetHelpTemplate("{{if or .Runnable .HasSubCommands}}{{.UsageString}}{{end}}" +
		"{{with (or .Long .Short)}}{{if not (or $.Runnable $.HasSubCommands)}}{{. | trimTrailingWhitespaces}}\n\n{{else}}" +
		"\n{{. | trimTrailingWhitespaces}}\n{{end}}{{end}}")

	root.AddGroup(&cobra.Group{ID: "main", Title: "Commands:"})

	authCmd := cmd.NewAuthCmd(
		makeLoginRunE(stdout, stderr, false),
		makeLoginRunE(stdout, stderr, true),
	)

	// Wrap non-browser subcommands with the Layer 2 presenter.
	// login/reauth apply the presenter inline; skip them here.
	skipPresenter := map[string]bool{"login": true, "reauth": true}
	for _, sub := range authCmd.Commands() {
		if !skipPresenter[sub.Name()] {
			WrapWithPresenter(sub, stdout, stderr)
		}
	}

	authCmd.GroupID = "main"
	root.AddCommand(authCmd)

	readCmd := cmd.NewReadCmd()
	WrapWithPresenter(readCmd, stdout, stderr)
	readCmd.GroupID = "main"
	root.AddCommand(readCmd)

	searchCmd := cmd.NewSearchCmd()
	WrapWithPresenter(searchCmd, stdout, stderr)
	searchCmd.GroupID = "main"
	root.AddCommand(searchCmd)

	activityCmd := cmd.NewActivityCmd()
	WrapWithPresenter(activityCmd, stdout, stderr)
	activityCmd.GroupID = "main"
	root.AddCommand(activityCmd)

	historyCmd := cmd.NewHistoryCmd()
	WrapWithPresenter(historyCmd, stdout, stderr)
	historyCmd.GroupID = "main"
	root.AddCommand(historyCmd)


	sendCmd := cmd.NewSendCmd()
	WrapWithPresenter(sendCmd, stdout, stderr)
	sendCmd.GroupID = "main"
	root.AddCommand(sendCmd)

	reactCmd := cmd.NewReactCmd()
	WrapWithPresenter(reactCmd, stdout, stderr)
	reactCmd.GroupID = "main"
	root.AddCommand(reactCmd)

	deleteCmd := cmd.NewDeleteCmd()
	WrapWithPresenter(deleteCmd, stdout, stderr)
	deleteCmd.GroupID = "main"
	root.AddCommand(deleteCmd)

	forwardCmd := cmd.NewForwardCmd()
	WrapWithPresenter(forwardCmd, stdout, stderr)
	forwardCmd.GroupID = "main"
	root.AddCommand(forwardCmd)

	liveCmd := cmd.NewLiveCmd(makeLiveRunE(stdout, stderr))
	// Wrap the types subcommand; the live command itself uses inline presenter.
	for _, sub := range liveCmd.Commands() {
		WrapWithPresenter(sub, stdout, stderr)
	}
	liveCmd.GroupID = "main"
	root.AddCommand(liveCmd)

	snippetCmd := cmd.NewSnippetCmd()
	// Wrap all three subcommands (create, delete, types) with the presenter.
	for _, sub := range snippetCmd.Commands() {
		WrapWithPresenter(sub, stdout, stderr)
	}
	snippetCmd.GroupID = "main"
	root.AddCommand(snippetCmd)
	return root
}

// ---------------------------------------------------------------------------
// Layer 1 → Layer 2 bridge for auth login / reauth
//
// These commands need OS signals and Playwright, so their RunE is built here
// rather than in cmd/. The presenter is applied inline because timing starts
// before the browser opens.
// ---------------------------------------------------------------------------

func makeLoginRunE(stdout, stderr io.Writer, isReauth bool) func(*cobra.Command, []string) error {
	return func(c *cobra.Command, _ []string) error {
		workspace, _ := c.Flags().GetString("workspace")
		firefox, _ := c.Flags().GetBool("firefox")

		bt := browser.Chromium
		if firefox {
			bt = browser.Firefox
		}

		if isReauth {
			ws := cmd.CanonicalDomain(workspace)
			if delErr := keychain.Delete(ws); delErr != nil && !errors.Is(delErr, keychain.ErrNotFound) {
				return fmt.Errorf("clearing existing credentials: %w", delErr)
			}
		}

		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		go func() {
			<-ctx.Done()
			stop() // second Ctrl+C kills immediately
			time.Sleep(3 * time.Second)
			os.Exit(130)
		}()

		start := time.Now()
		slog.Info("opening browser", "workspace", workspace)

		creds, err := browser.Extract(ctx, workspace, browser.Options{Browser: bt})
		elapsed := time.Since(start)

		if err != nil {
			output.Print(stdout, stderr, output.Result{
				Stderr:   err.Error(),
				ExitCode: 1,
				Elapsed:  elapsed,
			})
			return errAlreadyPresented
		}

		entry := keychain.Entry{
			Workspace: creds.Workspace,
			Token:     creds.Token,
			Cookie:    creds.Cookie,
		}
		if saveErr := keychain.Save(entry); saveErr != nil {
			output.Print(stdout, stderr, output.Result{
				Stderr:   saveErr.Error(),
				ExitCode: 1,
				Elapsed:  elapsed,
			})
			return errAlreadyPresented
		}

		// Attempt to discover Enterprise Grid sibling workspaces.  This is best-
		// effort: failures are logged but do not prevent login from succeeding.
		slackClient := slack.NewClient(creds.Token, creds.Cookie)
		if at, authErr := slackClient.AuthTest(); authErr == nil && at.OK && at.EnterpriseID != "" {
			if grids, gridErr := slackClient.GridWorkspaces(ctx, creds.Workspace); gridErr == nil {
				domains := make([]string, len(grids))
				for i, g := range grids {
					domains[i] = g.Domain + ".slack.com"
				}
				entry.EnterpriseID = at.EnterpriseID
				entry.GridWorkspaces = domains
				_ = keychain.Save(entry) // re-save with grid metadata; non-fatal on failure
			}
		}

		msg := fmt.Sprintf("Credentials saved to Keychain for workspace %q\n", creds.Workspace)
		fmt.Fprint(stdout, output.Format(output.Result{
			Stdout:  msg,
			Elapsed: elapsed,
		}))
		return nil
	}
}

// ---------------------------------------------------------------------------
// Layer 1 → Layer 2 bridge for non-browser commands
//
// Cobra's Execute() calls RunE, which writes raw output to cmd.OutOrStdout().
// We capture that output with a bytes.Buffer, measure elapsed time, and apply
// the presenter — but only for commands that go through this path.
//
// For login/reauth the presenter is applied inline (above) because timing
// wraps the browser session.
//
// For status/logout the RunE in cmd/auth.go writes to c.OutOrStdout(), which
// we pre-wire to a capturing buffer below via PersistentPreRun/PersistentPostRun
// on the auth command group.
// ---------------------------------------------------------------------------

// WrapWithPresenter wraps a *cobra.Command's RunE so its output passes through
// the Layer 2 presenter. Call this on leaf commands that should have the footer.
//
// The wrapped command's OutOrStdout() must already be set to a *bytes.Buffer
// before Execute() runs so we can capture the output.
//
// --help bypasses RunE entirely: cobra writes help to cmd.OutOrStdout() and
// returns nil from Execute() without ever calling RunE. We intercept this by
// setting a custom HelpFunc that writes directly to finalOut so the caller
// always sees help output regardless of the buffer redirect.
func WrapWithPresenter(c *cobra.Command, finalOut io.Writer, finalErr io.Writer) {
	original := c.RunE
	if original == nil {
		return
	}
	var buf bytes.Buffer
	c.SetOut(&buf)

	// Cobra's --help handler calls c.HelpFunc()(c, args) and writes the result
	// to c.OutOrStdout(). Since we redirect that to buf, help would be swallowed.
	// Override HelpFunc: temporarily point the command's output at finalOut so
	// cobra's default template (usage + flags + Long) is written there directly.
	defaultHelp := c.HelpFunc()
	c.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		cmd.SetOut(finalOut)
		defaultHelp(cmd, args)
		cmd.SetOut(&buf)
	})

	c.RunE = func(cmd *cobra.Command, args []string) error {
		start := time.Now()
		err := original(cmd, args)
		elapsed := time.Since(start)

		// JSON mode: bypass the presenter entirely.
		// NDJSON consumers use exit code for errors; the footer would corrupt the stream.
		if jsonMode, _ := cmd.Flags().GetBool("json"); jsonMode {
			if buf.Len() > 0 {
				fmt.Fprint(finalOut, buf.String())
			}
			if err != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), err.Error())
				return errAlreadyPresented
			}
			return nil
		}

		exitCode := 0
		stderrStr := ""
		if err != nil {
			exitCode = 1
			stderrStr = err.Error()
			// Print help before the error so the caller knows what to supply.
			// HelpFunc writes directly to finalOut (see SetHelpFunc above).
			cmd.HelpFunc()(cmd, args)
			fmt.Fprintln(finalOut)
		}

		output.Print(finalOut, finalErr, output.Result{
			Stdout:   buf.String(),
			Stderr:   stderrStr,
			ExitCode: exitCode,
			Elapsed:  elapsed,
		})

		// Return errAlreadyPresented: we've written the formatted output and
		// need run() to exit non-zero, but without writing a second presenter block.
		if err != nil {
			return errAlreadyPresented
		}
		return nil
	}
}

// ---------------------------------------------------------------------------
// Layer 1 → Layer 2 bridge for live
//
// The live command streams events directly to stdout without buffering, so it
// cannot use WrapWithPresenter.  The presenter footer is emitted once on exit
// (or on fatal error), exactly like makeLoginRunE.
//
// Reconnection: on WebSocket disconnect we retry up to 3 times with
// exponential backoff (1 s, 3 s, 9 s) before giving up.
// ---------------------------------------------------------------------------

func makeLiveRunE(stdout, stderr io.Writer) func(*cobra.Command, []string) error {
	return func(c *cobra.Command, _ []string) error {
		workspace, _ := c.Flags().GetString("workspace")
		channels, _ := c.Flags().GetStringArray("channel")
		fromUser, _ := c.Flags().GetString("from")
		types, _ := c.Flags().GetStringArray("type")
		jsonMode, _ := c.Flags().GetBool("json")
		mentionMode, _ := c.Flags().GetBool("mention")

		if workspace == "" {
			var wsErr error
			workspace, wsErr = keychain.ResolveDefault()
			if wsErr != nil {
			output.Print(stdout, stderr, output.Result{
				Stderr:   fmt.Sprintf("%v\nRun: slackcli auth default --workspace <name>", wsErr),
				ExitCode: 1,
				Elapsed:  0,
			})
				return errAlreadyPresented
			}
		} else {
			workspace = cmd.CanonicalDomain(workspace)
		}

		entry, err := keychain.Load(workspace)
		if err != nil {
			output.Print(stdout, stderr, output.Result{
				Stderr:   fmt.Sprintf("credentials not found for %s — run: slackcli auth login --workspace %s\ndetail: %v", workspace, workspace, err),
				ExitCode: 1,
				Elapsed:  0,
			})
			return errAlreadyPresented
		}

		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		go func() {
			<-ctx.Done()
			stop() // second Ctrl+C kills immediately
			time.Sleep(3 * time.Second)
			os.Exit(130)
		}()

		slackClient := slack.NewClient(entry.Token, entry.Cookie)

		// Fetch gateway server once; reuse on reconnect.
		gatewayServer, err := slackClient.GatewayServer(ctx, workspace)
		if err != nil {
			output.Print(stdout, stderr, output.Result{
				Stderr:   fmt.Sprintf("obtaining gateway server: %v", err),
				ExitCode: 1,
				Elapsed:  0,
			})
			return errAlreadyPresented
		}

		filter := cmd.LiveFilter{
			Channels: channels,
			FromUser: fromUser,
			Types:    types,
		}

		// --mention: resolve our own user ID via auth.test so we can filter
		// on events that contain <@OURUID> in the text.
		if mentionMode {
			authResult, authErr := slackClient.AuthTest()
			if authErr != nil || !authResult.OK {
				detail := ""
				if authErr != nil {
					detail = authErr.Error()
				} else {
					detail = authResult.Error
				}
			output.Print(stdout, stderr, output.Result{
				Stderr:   fmt.Sprintf("auth.test failed (required for --mention): %s", detail),
				ExitCode: 1,
				Elapsed:  0,
			})
				return errAlreadyPresented
			}
			filter.SelfUserID = authResult.UserID
			selfID := authResult.UserID
			filter.IsThreadParticipant = func(channelID, threadTs string) bool {
				root, err := slackClient.GetMessage(channelID, threadTs)
				if err != nil {
					// Fail open: can't fetch → assume not a participant.
					slog.Warn("could not fetch thread root for mention filter", "channel", channelID, "ts", threadTs, "err", err)
					return false
				}
				for _, u := range root.ReplyUsers {
					if u == selfID {
						return true
					}
				}
				return false
			}
		}

		// User cache and channel names are populated lazily; nil is safe for
		// FormatEvent (falls back to raw IDs).
		userCache, ucErr := slack.NewUserCache(workspace, slackClient)
		if ucErr != nil {
			slog.Warn("could not load user cache", "err", ucErr)
			userCache = nil
		}

		// chanNames maps channel ID → name (without #). Populated on first
		// sight of each channel ID so --channel name filtering works without
		// a bulk conversations.list call at startup.
		chanNames := make(map[string]string)

		start := time.Now()

		const maxRetries = 3
		backoff := time.Second
		attempt := 0

		var streamErr error
	retry:
		for attempt <= maxRetries {
			if attempt > 0 {
				fmt.Fprintf(stderr, "reconnecting (attempt %d/%d)...\n", attempt, maxRetries)
				select {
				case <-ctx.Done():
					streamErr = ctx.Err()
					break retry
				case <-time.After(backoff):
				}
				backoff *= 3
			}

			ws, dialErr := slackClient.DialWebSocket(ctx, gatewayServer)
			if dialErr != nil {
				slog.Warn("websocket dial failed", "attempt", attempt, "err", dialErr)
				attempt++
				continue
			}

			for {
				e, readErr := ws.ReadEvent(ctx)
				if readErr != nil {
					_ = ws.Close()
					if readErr == context.Canceled || readErr == context.DeadlineExceeded {
						streamErr = nil // clean exit via signal
						break retry
					}
					slog.Warn("websocket read error", "err", readErr)
					attempt++
					continue retry
				}

				// Reset reconnect counter on a successful read.
				attempt = 0
				backoff = time.Second

				// Resolve channel name on first sight so --channel name filters work.
				if e.Channel != "" {
					if _, known := chanNames[e.Channel]; !known {
						if name, err := slackClient.GetChannelName(ctx, e.Channel); err == nil {
							chanNames[e.Channel] = name
						} else {
							chanNames[e.Channel] = "" // don't retry on error
							slog.Debug("could not resolve channel name", "id", e.Channel, "err", err)
						}
					}
				}

				if !filter.Accept(e, userCache, chanNames) {
					continue
				}

				if jsonMode {
					fmt.Fprintln(stdout, cmd.FormatEventJSON(e, userCache, chanNames))
				} else {
					fmt.Fprint(stdout, cmd.FormatEvent(e, userCache, chanNames))
					fmt.Fprintln(stdout)
				}
			}
		}

		exitCode := 0
		stderrMsg := ""
		if streamErr != nil {
			exitCode = 1
			stderrMsg = streamErr.Error()
		}
		if attempt > maxRetries {
			exitCode = 1
			stderrMsg = fmt.Sprintf("websocket disconnected after %d reconnect attempts", maxRetries)
		}

		// JSON mode: no presenter footer. Errors go to stderr; clean exit emits nothing.
		if jsonMode {
			if stderrMsg != "" {
				fmt.Fprintln(stderr, stderrMsg)
				return errAlreadyPresented
			}
			return nil
		}

		output.Print(stdout, stderr, output.Result{
			Stderr:   stderrMsg,
			ExitCode: exitCode,
			Elapsed:  time.Since(start),
		})
		return errAlreadyPresented
	}
}
