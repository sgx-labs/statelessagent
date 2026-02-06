package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sgx-labs/statelessagent/internal/cli"
)

func ciCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ci",
		Short: "Set up continuous integration for your project",
		Long: `CI (Continuous Integration) automatically runs tasks when you push code.

This command helps you set up GitHub Actions to:
- Run tests on every push
- Build releases when you create a tag
- Catch bugs before they reach production

Run 'same ci init' to get started.`,
	}

	cmd.AddCommand(ciInitCmd())
	cmd.AddCommand(ciExplainCmd())

	return cmd
}

func ciInitCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create a GitHub Actions workflow for your project",
		Long: `Detects your project type and creates an appropriate CI workflow.

Supported project types:
- Go (go.mod)
- Node.js (package.json)
- Python (requirements.txt, pyproject.toml)
- Generic (fallback)

The workflow will be created at .github/workflows/ci.yml`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCIInit(force)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "Overwrite existing workflow")
	return cmd
}

func ciExplainCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "explain",
		Short: "Learn what CI is and why it's useful",
		Run: func(cmd *cobra.Command, args []string) {
			printCIExplanation()
		},
	}
}

func runCIInit(force bool) error {
	// Check if we're in a git repo
	if _, err := exec.Command("git", "rev-parse", "--show-toplevel").Output(); err != nil {
		return fmt.Errorf("not a git repository. Run 'git init' first")
	}

	// Check for existing workflow
	workflowPath := ".github/workflows/ci.yml"
	if _, err := os.Stat(workflowPath); err == nil && !force {
		return fmt.Errorf("CI workflow already exists at %s. Use --force to overwrite", workflowPath)
	}

	// Detect project type
	projectType := detectProjectType()

	fmt.Printf("\n%sWhat is CI?%s\n", cli.Bold, cli.Reset)
	fmt.Printf("  CI (Continuous Integration) automatically runs tasks when you push code.\n")
	fmt.Printf("  It catches bugs early and ensures your code always works.\n\n")

	fmt.Printf("%sDetected project type:%s %s%s%s\n\n", cli.Bold, cli.Reset, cli.Cyan, projectType, cli.Reset)

	// Generate workflow
	workflow := generateWorkflow(projectType)

	// Create directory
	if err := os.MkdirAll(".github/workflows", 0o755); err != nil {
		return fmt.Errorf("create workflows directory: %w", err)
	}

	// Write workflow
	if err := os.WriteFile(workflowPath, []byte(workflow), 0o644); err != nil {
		return fmt.Errorf("write workflow: %w", err)
	}

	fmt.Printf("%s✓%s Created %s\n\n", cli.Green, cli.Reset, workflowPath)

	fmt.Printf("%sWhat happens now:%s\n", cli.Bold, cli.Reset)
	fmt.Printf("  1. Commit this file: git add .github && git commit -m \"Add CI workflow\"\n")
	fmt.Printf("  2. Push to GitHub: git push\n")
	fmt.Printf("  3. GitHub will automatically run your tests on every push!\n\n")

	fmt.Printf("%sView your CI runs:%s\n", cli.Bold, cli.Reset)
	fmt.Printf("  Go to your repo on GitHub → Actions tab\n\n")

	fmt.Printf("%sLearn more:%s same ci explain\n\n", cli.Dim, cli.Reset)

	return nil
}

func detectProjectType() string {
	checks := []struct {
		file     string
		projType string
	}{
		{"go.mod", "go"},
		{"package.json", "node"},
		{"requirements.txt", "python"},
		{"pyproject.toml", "python"},
		{"Cargo.toml", "rust"},
		{"Gemfile", "ruby"},
	}

	for _, c := range checks {
		if _, err := os.Stat(c.file); err == nil {
			return c.projType
		}
	}

	return "generic"
}

func generateWorkflow(projectType string) string {
	switch projectType {
	case "go":
		return goWorkflow()
	case "node":
		return nodeWorkflow()
	case "python":
		return pythonWorkflow()
	default:
		return genericWorkflow()
	}
}

func goWorkflow() string {
	return strings.TrimSpace(`
# CI workflow for Go projects
# Created by: same ci init
# Learn more: same ci explain

name: CI

on:
  push:
    branches: [main, master]
  pull_request:
    branches: [main, master]

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Set up Go
        uses: actions/setup-go@v5
        with:
          go-version: '1.23'

      - name: Run tests
        run: go test ./... -v

      - name: Build
        run: go build ./...
`) + "\n"
}

func nodeWorkflow() string {
	return strings.TrimSpace(`
# CI workflow for Node.js projects
# Created by: same ci init
# Learn more: same ci explain

name: CI

on:
  push:
    branches: [main, master]
  pull_request:
    branches: [main, master]

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Set up Node.js
        uses: actions/setup-node@v4
        with:
          node-version: '20'
          cache: 'npm'

      - name: Install dependencies
        run: npm ci

      - name: Run tests
        run: npm test

      - name: Build
        run: npm run build --if-present
`) + "\n"
}

func pythonWorkflow() string {
	return strings.TrimSpace(`
# CI workflow for Python projects
# Created by: same ci init
# Learn more: same ci explain

name: CI

on:
  push:
    branches: [main, master]
  pull_request:
    branches: [main, master]

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Set up Python
        uses: actions/setup-python@v5
        with:
          python-version: '3.12'

      - name: Install dependencies
        run: |
          python -m pip install --upgrade pip
          pip install -r requirements.txt || pip install -e .
          pip install pytest

      - name: Run tests
        run: pytest -v || python -m pytest -v || echo "No tests found"
`) + "\n"
}

func genericWorkflow() string {
	return strings.TrimSpace(`
# CI workflow (generic)
# Created by: same ci init
# Learn more: same ci explain
#
# This is a starter workflow. Customize it for your project!

name: CI

on:
  push:
    branches: [main, master]
  pull_request:
    branches: [main, master]

jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Build
        run: |
          echo "Add your build commands here"
          # Examples:
          # make build
          # npm run build
          # go build ./...

      - name: Test
        run: |
          echo "Add your test commands here"
          # Examples:
          # make test
          # npm test
          # go test ./...
`) + "\n"
}

func printCIExplanation() {
	fmt.Printf(`
%sWhat is CI (Continuous Integration)?%s

CI is like having a robot assistant that checks your code every time you push.

%sWhat it does:%s
  • Runs your tests automatically
  • Catches bugs before they reach production
  • Ensures code works on a clean machine (not just yours)
  • Builds releases when you tag a version

%sHow it works:%s
  1. You push code to GitHub
  2. GitHub sees your .github/workflows/*.yml files
  3. It spins up a fresh computer and runs your commands
  4. You see ✓ green (passed) or ✗ red (failed) on your commits

%sWhy vibe coders should use it:%s
  • You don't have to remember to run tests
  • It catches "works on my machine" bugs
  • It looks professional (green checkmarks!)
  • It's free for public repos

%sCommon workflows:%s
  • %sci.yml%s — Run tests on every push
  • %srelease.yml%s — Build binaries when you tag a version
  • %sdeploy.yml%s — Deploy to production automatically

%sGet started:%s same ci init

`, cli.Bold, cli.Reset,
		cli.Bold, cli.Reset,
		cli.Bold, cli.Reset,
		cli.Bold, cli.Reset,
		cli.Bold, cli.Reset,
		cli.Cyan, cli.Reset,
		cli.Cyan, cli.Reset,
		cli.Cyan, cli.Reset,
		cli.Dim, cli.Reset)
}
