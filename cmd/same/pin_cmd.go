package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sgx-labs/statelessagent/internal/cli"
	"github.com/sgx-labs/statelessagent/internal/config"
	"github.com/sgx-labs/statelessagent/internal/store"
)

func pinCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pin",
		Short: "Always include a note in every session",
		Long: `Pin important notes so they're always included when your AI starts a session.

Pinned notes are injected every time, regardless of what you're working on.
Use this for architecture decisions, coding standards, or project context
that your AI should always know about.

  same pin path/to/note.md      Pin a note
  same pin list                 Show all pinned notes
  same pin remove path/to/note  Unpin a note`,
	}

	cmd.AddCommand(pinAddCmd())
	cmd.AddCommand(pinListCmd())
	cmd.AddCommand(pinRemoveCmd())

	// Allow `same pin <path>` as shorthand for `same pin add <path>`
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) == 1 {
			return runPinAdd(args[0])
		}
		return cmd.Help()
	}
	// Accept arbitrary args so `same pin path/to/note.md` works
	cmd.Args = cobra.ArbitraryArgs

	return cmd
}

func pinAddCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "add [path]",
		Short: "Pin a note",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPinAdd(args[0])
		},
	}
}

func pinListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Show all pinned notes",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPinList()
		},
	}
}

func pinRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove [path]",
		Short: "Unpin a note",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPinRemove(args[0])
		},
	}
}

func runPinAdd(path string) error {
	db, err := store.Open()
	if err != nil {
		return config.ErrNoDatabase
	}
	defer db.Close()

	// Check if note exists in the index
	notes, err := db.GetNoteByPath(path)
	if err != nil || len(notes) == 0 {
		return fmt.Errorf("note not found in index: %s\n  Make sure the path is relative to your vault root", path)
	}

	already, _ := db.IsPinned(path)
	if already {
		fmt.Printf("  Already pinned: %s\n", path)
		return nil
	}

	if err := db.PinNote(path); err != nil {
		return fmt.Errorf("pin note: %w", err)
	}

	fmt.Printf("  %s✓%s Pinned: %s\n", cli.Green, cli.Reset, notes[0].Title)
	fmt.Printf("    %sThis note will be included in every session%s\n", cli.Dim, cli.Reset)
	return nil
}

func runPinList() error {
	db, err := store.Open()
	if err != nil {
		return config.ErrNoDatabase
	}
	defer db.Close()

	paths, err := db.GetPinnedPaths()
	if err != nil {
		return fmt.Errorf("get pinned notes: %w", err)
	}

	if len(paths) == 0 {
		fmt.Println("  No pinned notes.")
		fmt.Printf("  %sPin a note with: same pin path/to/note.md%s\n", cli.Dim, cli.Reset)
		return nil
	}

	fmt.Printf("  %sPinned notes%s (always included in sessions):\n\n", cli.Bold, cli.Reset)
	for _, p := range paths {
		notes, _ := db.GetNoteByPath(p)
		title := p
		if len(notes) > 0 {
			title = notes[0].Title
		}
		fmt.Printf("    %s %s\n", title, cli.Dim+p+cli.Reset)
	}
	fmt.Printf("\n  %d pinned note(s).\n", len(paths))
	return nil
}

func runPinRemove(path string) error {
	db, err := store.Open()
	if err != nil {
		return config.ErrNoDatabase
	}
	defer db.Close()

	if err := db.UnpinNote(path); err != nil {
		return err
	}

	fmt.Printf("  %s✓%s Unpinned: %s\n", cli.Green, cli.Reset, path)
	return nil
}
