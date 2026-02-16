package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sgx-labs/statelessagent/internal/cli"
	"github.com/sgx-labs/statelessagent/internal/config"
	"github.com/sgx-labs/statelessagent/internal/graph"
	"github.com/sgx-labs/statelessagent/internal/store"
)

func graphCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "graph",
		Short: "Interact with the knowledge graph",
		Long:  "Query, explore, and manage the knowledge graph.",
	}

	cmd.AddCommand(graphStatsCmd())
	cmd.AddCommand(graphQueryCmd())
	cmd.AddCommand(graphPathCmd())
	cmd.AddCommand(graphRebuildCmd())

	return cmd
}

func graphStatsCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Show graph statistics",
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := store.Open()
			if err != nil {
				return config.ErrNoDatabase
			}
			defer db.Close()

			gdb := graph.NewDB(db.Conn())
			stats, err := gdb.GetStats()
			if err != nil {
				return fmt.Errorf("get stats: %w", err)
			}

			if jsonOut {
				data, _ := json.MarshalIndent(stats, "", "  ")
				fmt.Println(string(data))
				return nil
			}

			fmt.Printf("Graph Statistics:\n")
			fmt.Printf("  Nodes: %d\n", stats.TotalNodes)
			fmt.Printf("  Edges: %d\n", stats.TotalEdges)
			if stats.TotalNodes > 0 {
				fmt.Printf("  Avg Degree: %.2f\n", stats.AvgDegree)
			}
			fmt.Println("\nNodes by Type:")
			for t, c := range stats.NodesByType {
				fmt.Printf("  %s: %d\n", t, c)
			}
			fmt.Println("\nEdges by Relationship:")
			for r, c := range stats.EdgesByRelationship {
				fmt.Printf("  %s: %d\n", r, c)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output as JSON")
	return cmd
}

func graphQueryCmd() *cobra.Command {
	var (
		nodeName string
		nodeType string
		rel      string
		depth    int
		dir      string
		jsonOut  bool
	)
	cmd := &cobra.Command{
		Use:   "query",
		Short: "Query the graph from a start node",
		Example: `  same graph query --type note --node "internal/store/db.go" --depth 2
  same graph query --type decision --node "Use SQLite" --dir reverse`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if nodeName == "" {
				return fmt.Errorf("--node is required")
			}
			if nodeType == "" {
				return fmt.Errorf("--type is required (note, file, agent, decision, etc.)")
			}

			db, err := store.Open()
			if err != nil {
				return config.ErrNoDatabase
			}
			defer db.Close()

			gdb := graph.NewDB(db.Conn())

			opts := graph.QueryOptions{
				FromNodeType: nodeType,
				FromNodeName: nodeName,
				Relationship: rel,
				MaxDepth:     depth,
				Direction:    dir,
			}

			paths, err := gdb.QueryGraph(opts)
			if err != nil {
				return err
			}

			if jsonOut {
				data, _ := json.MarshalIndent(paths, "", "  ")
				fmt.Println(string(data))
				return nil
			}

			if len(paths) == 0 {
				fmt.Println("No results found.")
				return nil
			}

			fmt.Printf("Found %d paths:\n", len(paths))
			for i, p := range paths {
				fmt.Printf("\n%sPath %d (Length %d):%s\n", cli.Bold, i+1, len(p.Nodes), cli.Reset)
				for j, n := range p.Nodes {
					prefix := "  "
					if j > 0 {
						prefix = "  -> "
					}
					fmt.Printf("%s[%s] %s%s%s\n", prefix, n.Type, cli.Cyan, n.Name, cli.Reset)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&nodeName, "node", "", "Name of the start node")
	cmd.Flags().StringVar(&nodeType, "type", "note", "Type of the start node")
	cmd.Flags().StringVar(&rel, "rel", "", "Filter by relationship type")
	cmd.Flags().IntVar(&depth, "depth", 1, "Traversal depth")
	cmd.Flags().StringVar(&dir, "dir", "forward", "Direction (forward, reverse)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output as JSON")
	return cmd
}

func graphPathCmd() *cobra.Command {
	var (
		fromName string
		fromType string
		toName   string
		toType   string
		jsonOut  bool
	)
	cmd := &cobra.Command{
		Use:   "path",
		Short: "Find the shortest path between two nodes",
		RunE: func(cmd *cobra.Command, args []string) error {
			if fromName == "" || toName == "" {
				return fmt.Errorf("--from and --to are required")
			}

			db, err := store.Open()
			if err != nil {
				return config.ErrNoDatabase
			}
			defer db.Close()

			gdb := graph.NewDB(db.Conn())

			// Resolve start node
			startNode, err := gdb.FindNode(fromType, fromName)
			if err != nil {
				return fmt.Errorf("start node not found: %w", err)
			}

			// Resolve end node
			endNode, err := gdb.FindNode(toType, toName)
			if err != nil {
				return fmt.Errorf("end node not found: %w", err)
			}

			path, err := gdb.FindShortestPath(startNode.ID, endNode.ID)
			if err != nil {
				return fmt.Errorf("search failed: %w", err)
			}

			if jsonOut {
				data, _ := json.MarshalIndent(path, "", "  ")
				fmt.Println(string(data))
				return nil
			}

			if path == nil {
				fmt.Println("No path found.")
				return nil
			}

			fmt.Printf("Shortest path (%d steps):\n", len(path.Nodes)-1)
			for i, n := range path.Nodes {
				prefix := "  "
				if i > 0 {
					// See if we have an edge to display
					// path.Edges has length len(Nodes)-1
					rel := ""
					if i-1 < len(path.Edges) {
						rel = fmt.Sprintf(" --[%s]--> ", path.Edges[i-1].Relationship)
					} else {
						rel = " -> "
					}
					prefix = " " + rel
				}
				fmt.Printf("%s[%s] %s%s%s\n", prefix, n.Type, cli.Cyan, n.Name, cli.Reset)
			}

			return nil
		},
	}
	cmd.Flags().StringVar(&fromName, "from", "", "Name of start node")
	cmd.Flags().StringVar(&fromType, "from-type", "note", "Type of start node")
	cmd.Flags().StringVar(&toName, "to", "", "Name of end node")
	cmd.Flags().StringVar(&toType, "to-type", "note", "Type of end node")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output as JSON")
	return cmd
}

func graphRebuildCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rebuild",
		Short: "Rebuild the basic graph structure",
		Long:  "Populate the graph from existing notes (notes, agents, produced relationships). Does not re-run text extraction.",
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := store.Open()
			if err != nil {
				return config.ErrNoDatabase
			}
			defer db.Close()

			fmt.Println("Rebuilding basic graph structure...")
			if err := graph.PopulateFromExistingNotes(db.Conn()); err != nil {
				return err
			}
			fmt.Println("Done.")
			return nil
		},
	}
}
