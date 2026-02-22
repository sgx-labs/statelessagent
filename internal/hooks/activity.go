package hooks

import "strings"

const (
	hookStatusInjected = "injected"
	hookStatusSkipped  = "skipped"
	hookStatusEmpty    = "empty"
	hookStatusError    = "error"
)

type hookRunResult struct {
	Output          *HookOutput
	Status          string
	NotesCount      int
	EstimatedTokens int
	ErrorMessage    string
	Detail          string
	NotePaths       []string
}

func hookInjected(output *HookOutput, notesCount, estimatedTokens int, paths []string, detail string) hookRunResult {
	return hookRunResult{
		Output:          output,
		Status:          hookStatusInjected,
		NotesCount:      max(notesCount, 0),
		EstimatedTokens: max(estimatedTokens, 0),
		Detail:          strings.TrimSpace(detail),
		NotePaths:       paths,
	}
}

func hookSkipped(detail string) hookRunResult {
	return hookRunResult{
		Status: hookStatusSkipped,
		Detail: strings.TrimSpace(detail),
	}
}

func hookEmpty(detail string) hookRunResult {
	return hookRunResult{
		Status: hookStatusEmpty,
		Detail: strings.TrimSpace(detail),
	}
}

func hookError(errMsg string) hookRunResult {
	return hookRunResult{
		Status:       hookStatusError,
		ErrorMessage: strings.TrimSpace(errMsg),
	}
}

func normalizeHookResult(result hookRunResult) hookRunResult {
	if result.Status == "" {
		if result.ErrorMessage != "" {
			result.Status = hookStatusError
		} else if result.Output != nil {
			result.Status = hookStatusInjected
		} else {
			result.Status = hookStatusEmpty
		}
	}
	if result.NotesCount < 0 {
		result.NotesCount = 0
	}
	if result.EstimatedTokens < 0 {
		result.EstimatedTokens = 0
	}
	result.Detail = strings.TrimSpace(result.Detail)
	result.ErrorMessage = strings.TrimSpace(result.ErrorMessage)
	return result
}
