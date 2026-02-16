package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sgx-labs/statelessagent/internal/config"
	"github.com/sgx-labs/statelessagent/internal/store"
)

func claimCmd() *cobra.Command {
	var (
		agent       string
		ttl         time.Duration
		readPath    string
		writePath   string
		listClaims  bool
		releasePath string
	)

	cmd := &cobra.Command{
		Use:   "claim [file]",
		Short: "Coordinate file ownership across agents",
		Long: `Create advisory read/write claims so multiple agents can coordinate safely.

Examples:
  same claim cmd/same/main.go --agent codex
  same claim --read internal/store/search.go --agent claude
  same claim --list
  same claim --release cmd/same/main.go`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch {
			case listClaims:
				return runClaimList()
			case releasePath != "":
				return runClaimRelease(releasePath, agent)
			case readPath != "":
				return runClaimUpsert(readPath, agent, store.ClaimTypeRead, ttl)
			case writePath != "":
				return runClaimUpsert(writePath, agent, store.ClaimTypeWrite, ttl)
			case len(args) == 1:
				return runClaimUpsert(args[0], agent, store.ClaimTypeWrite, ttl)
			default:
				return cmd.Help()
			}
		},
	}

	cmd.Flags().StringVar(&agent, "agent", "", "Agent name (required for claim/release by-agent)")
	cmd.Flags().DurationVar(&ttl, "ttl", store.DefaultClaimTTL, "Claim TTL before auto-expiry (e.g. 30m, 1h)")
	cmd.Flags().StringVar(&readPath, "read", "", "Declare a read dependency on a file")
	cmd.Flags().StringVar(&writePath, "write", "", "Declare a write claim on a file")
	cmd.Flags().BoolVar(&listClaims, "list", false, "List active claims")
	cmd.Flags().StringVar(&releasePath, "release", "", "Release claims for a file")

	return cmd
}

func runClaimUpsert(path, agent, claimType string, ttl time.Duration) error {
	db, err := store.Open()
	if err != nil {
		return config.ErrNoDatabase
	}
	defer db.Close()

	cleanPath, err := store.NormalizeClaimPath(path)
	if err != nil {
		return fmt.Errorf("invalid path %q: %w", path, err)
	}
	if _, ok := config.SafeVaultSubpath(cleanPath); !ok {
		return fmt.Errorf("path %q is outside the current vault", cleanPath)
	}
	if strings.TrimSpace(agent) == "" {
		return fmt.Errorf("--agent is required (example: --agent codex)")
	}
	if ttl <= 0 {
		ttl = store.DefaultClaimTTL
	}

	if err := db.UpsertClaim(cleanPath, agent, claimType, ttl); err != nil {
		return fmt.Errorf("save claim: %w", err)
	}

	expires := time.Now().Add(ttl).Format("15:04:05")
	fmt.Printf("Claimed (%s): %s [agent=%s, ttl=%s, expires=%s]\n", claimType, cleanPath, strings.TrimSpace(agent), ttl.String(), expires)
	return nil
}

func runClaimList() error {
	db, err := store.Open()
	if err != nil {
		return config.ErrNoDatabase
	}
	defer db.Close()

	_, _ = db.PurgeExpiredClaims()
	claims, err := db.ListActiveClaims()
	if err != nil {
		return fmt.Errorf("list claims: %w", err)
	}
	if len(claims) == 0 {
		fmt.Println("No active claims.")
		fmt.Println("Create one with: same claim path/to/file --agent codex")
		return nil
	}

	fmt.Println("Active claims:")
	fmt.Printf("  %-7s %-14s %-42s %s\n", "TYPE", "AGENT", "PATH", "EXPIRES IN")
	for _, c := range claims {
		remaining := time.Until(time.Unix(c.ExpiresAt, 0))
		if remaining < 0 {
			remaining = 0
		}
		fmt.Printf("  %-7s %-14s %-42s %s\n", c.Type, c.Agent, c.Path, formatDuration(remaining))
	}
	return nil
}

func runClaimRelease(path, agent string) error {
	db, err := store.Open()
	if err != nil {
		return config.ErrNoDatabase
	}
	defer db.Close()

	cleanPath, err := store.NormalizeClaimPath(path)
	if err != nil {
		return fmt.Errorf("invalid path %q: %w", path, err)
	}
	if _, ok := config.SafeVaultSubpath(cleanPath); !ok {
		return fmt.Errorf("path %q is outside the current vault", cleanPath)
	}

	removed, err := db.ReleaseClaims(cleanPath, agent)
	if err != nil {
		return fmt.Errorf("release claim: %w", err)
	}
	if removed == 0 {
		if strings.TrimSpace(agent) == "" {
			fmt.Printf("No active claims found for %s\n", cleanPath)
		} else {
			fmt.Printf("No active claims found for %s [agent=%s]\n", cleanPath, strings.TrimSpace(agent))
		}
		return nil
	}

	if strings.TrimSpace(agent) == "" {
		fmt.Printf("Released %d claim(s) for %s\n", removed, cleanPath)
	} else {
		fmt.Printf("Released %d claim(s) for %s [agent=%s]\n", removed, cleanPath, strings.TrimSpace(agent))
	}
	return nil
}
