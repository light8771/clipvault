package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"clipboard_ext/internal/clipvault"
)

// Execute owns environment resolution and command lifecycle dispatch. The
// executable entry point only supplies process IO and reports fatal errors.
func Execute(args []string, stdout io.Writer) error {
	env, err := clipvault.NewEnvironment()
	if err != nil {
		return err
	}
	if len(args) == 0 {
		return clipvault.Run(env)
	}
	switch strings.ToLower(args[0]) {
	case "run":
		return clipvault.Run(env)
	case "search":
		return search(env, args[1:], stdout)
	case "data":
		_, err := fmt.Fprintln(stdout, env.DataDir)
		return err
	case "help", "-h", "--help":
		printUsage(stdout)
		return nil
	default:
		printUsage(stdout)
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func search(env clipvault.Environment, args []string, stdout io.Writer) error {
	instanceLock, err := clipvault.AcquireInstanceLock()
	if err != nil {
		return fmt.Errorf("cannot search while ClipVault is running or opening its data store: %w", err)
	}
	defer instanceLock.Close()
	store, err := clipvault.OpenStore(clipvault.StoreOptions{DataDir: env.DataDir})
	if err != nil {
		return err
	}
	defer store.Close()

	limit := 20
	queryParts := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-n", "--limit":
			if i+1 >= len(args) {
				return errors.New("missing limit value")
			}
			n, parseErr := strconv.Atoi(args[i+1])
			if parseErr != nil || n <= 0 {
				return fmt.Errorf("invalid limit %q", args[i+1])
			}
			limit = n
			i++
		default:
			queryParts = append(queryParts, args[i])
		}
	}
	opts := clipvault.SearchOptions{Query: strings.Join(queryParts, " "), Limit: limit}
	if clipvault.FreeTierRestriction(env) {
		opts.RestrictToLatestN = clipvault.FreeTierVisibleLimit
	}
	items, _, err := store.Search(context.Background(), opts)
	if err != nil {
		return err
	}
	for _, item := range items {
		text := strings.ReplaceAll(strings.ReplaceAll(item.Text, "\r\n", "\n"), "\n", "\\n")
		if len(text) > 120 {
			text = text[:120] + "..."
		}
		_, _ = fmt.Fprintf(stdout, "%d\t%s\t%s\t%d chars\t%s\n", item.ID, item.CreatedAt.Local().Format(time.DateTime), item.Kind, item.CharLen, text)
	}
	return nil
}

// Search is exposed for command-level lifecycle tests and alternate console
// hosts; normal callers use Execute.
func Search(env clipvault.Environment, args []string, stdout io.Writer) error {
	return search(env, args, stdout)
}

func printUsage(stdout io.Writer) {
	_, _ = fmt.Fprintln(stdout, `clipvault - Windows clipboard history recorder

Usage:
  clipvault                Run in tray mode
  clipvault run            Run in tray mode
  clipvault search [q]     Search clipboard history in the console
  clipvault data           Print data directory`)
}
