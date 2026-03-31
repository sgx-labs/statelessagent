package indexer

import "testing"

func TestFrontmatterProvenance_Parsed(t *testing.T) {
	content := `---
title: Imported Memory
provenance_source: /home/user/.claude/memory/memory.md
provenance_hash: abc123def456
---

This is an imported memory note.
`
	parsed := ParseNote(content)

	if parsed.Meta.ProvenanceSource != "/home/user/.claude/memory/memory.md" {
		t.Errorf("expected ProvenanceSource '/home/user/.claude/memory/memory.md', got %q", parsed.Meta.ProvenanceSource)
	}
	if parsed.Meta.ProvenanceHash != "abc123def456" {
		t.Errorf("expected ProvenanceHash 'abc123def456', got %q", parsed.Meta.ProvenanceHash)
	}
}

func TestFrontmatterProvenance_EmptyWhenNotPresent(t *testing.T) {
	content := `---
title: Normal Note
tags: [test]
---

This note has no provenance metadata.
`
	parsed := ParseNote(content)

	if parsed.Meta.ProvenanceSource != "" {
		t.Errorf("expected empty ProvenanceSource, got %q", parsed.Meta.ProvenanceSource)
	}
	if parsed.Meta.ProvenanceHash != "" {
		t.Errorf("expected empty ProvenanceHash, got %q", parsed.Meta.ProvenanceHash)
	}
}
