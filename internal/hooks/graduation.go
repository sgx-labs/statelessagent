package hooks

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/sgx-labs/statelessagent/internal/cli"
	"github.com/sgx-labs/statelessagent/internal/store"
)

// GraduationTip represents a tip to show when user reaches a milestone.
type GraduationTip struct {
	Key       string
	Condition func(db *store.DB) bool
	Message   func() string
}

// CheckGraduation checks if user has reached any milestones and shows tips.
// Returns the tip message if one should be shown, empty string otherwise.
func CheckGraduation(db *store.DB) string {
	tips := []GraduationTip{
		{
			Key:       store.MilestoneCIInit,
			Condition: shouldSuggestCI,
			Message:   ciTipMessage,
		},
		{
			Key:       store.MilestonePushProtect,
			Condition: shouldSuggestPushProtect,
			Message:   pushProtectTipMessage,
		},
	}

	for _, tip := range tips {
		if db.MilestoneShown(tip.Key) {
			continue
		}
		if tip.Condition(db) {
			db.RecordMilestone(tip.Key)
			return tip.Message()
		}
	}

	return ""
}

// shouldSuggestCI returns true if user has enough commits but no CI workflow.
func shouldSuggestCI(db *store.DB) bool {
	// Check if .github/workflows/ci.yml exists
	if _, err := os.Stat(".github/workflows/ci.yml"); err == nil {
		return false // Already has CI
	}

	// Check commit count
	commitCount := getCommitCount()
	return commitCount >= 10
}

// shouldSuggestPushProtect returns true if user might benefit from push protection.
func shouldSuggestPushProtect(db *store.DB) bool {
	// Check if already enabled
	if _, err := os.Stat(".git/hooks/pre-push"); err == nil {
		content, _ := os.ReadFile(".git/hooks/pre-push")
		if strings.Contains(string(content), "SAME Guard") {
			return false // Already has push protection
		}
	}

	// Suggest after some commits
	commitCount := getCommitCount()
	return commitCount >= 5
}

func getCommitCount() int {
	out, err := exec.Command("git", "rev-list", "--count", "HEAD").Output()
	if err != nil {
		return 0
	}
	var count int
	fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &count)
	return count
}

func ciTipMessage() string {
	return fmt.Sprintf(`
%s💡 Level up: Automate your tests%s

You've made 10+ commits — nice! Ready to automate?

  %ssame ci init%s — Set up GitHub Actions to run tests on every push

This catches bugs before they reach production.
`, cli.Cyan, cli.Reset, cli.Bold, cli.Reset)
}

func pushProtectTipMessage() string {
	return fmt.Sprintf(`
%s💡 Tip: Protect against wrong-repo pushes%s

Running multiple AI agents? Prevent accidental pushes:

  %ssame guard settings set push-protect on%s

This requires explicit authorization before each push.
`, cli.Cyan, cli.Reset, cli.Bold, cli.Reset)
}
