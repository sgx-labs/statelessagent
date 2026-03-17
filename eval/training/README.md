# Graph Extraction Training Data

Training and validation data for fine-tuning LLMs on SAME's graph extraction task.

## Files

| File | Examples | Description |
|------|----------|-------------|
| `graph_extraction_train_batch1.jsonl` | 15 | Based on eval/test_vault decision notes (3 variations each) |
| `graph_extraction_train_batch2.jsonl` | 15 | Synthetic developer notes (architecture, sprints, incidents) |
| `graph_extraction_train_batch3.jsonl` | 20 | Code review comments, PR descriptions, tech debt discussions, refactoring notes |
| `graph_extraction_train_batch4.jsonl` | 20 | API documentation, endpoint specs, integration guides, migration plans |
| `graph_extraction_train_batch5.jsonl` | 20 | Deployment runbooks, monitoring alerts, performance optimization, scaling decisions |
| `graph_extraction_train_batch6.jsonl` | 20 | Onboarding docs, team processes, coding standards, design system docs |
| `graph_extraction_val.jsonl` | 10 | Validation set (different content from training batches) |

**Total: 110 training + 10 validation = 120 examples**

## Format

Each line is a JSON object:

```json
{
  "instruction": "Extract entities and relationships from this developer note. Return JSON.",
  "input": "<developer note text>",
  "output": "<JSON matching LLMResponse format>"
}
```

## Output Schema

The output JSON matches `internal/graph/llm.go` LLMResponse:

```json
{
  "nodes": [
    {"type": "entity|decision|concept", "name": "..."}
  ],
  "edges": [
    {"source": "NodeName", "target": "NodeName", "relation": "affects|uses|related_to"}
  ]
}
```

### Node Types
- `decision` — Architectural or design decisions (e.g., "Use SQLite", "Adopt Test Pyramid")
- `entity` — Libraries, technologies, external systems (e.g., "Redis", "PostgreSQL")
- `concept` — Domain concepts (e.g., "Rate Limiting", "Observability")

### Edge Relations
- `affects` — A decision affects an entity or concept
- `uses` — An entity uses another entity
- `related_to` — General relationship

## Combining Batches

```bash
cat graph_extraction_train_batch{1,2,3,4,5,6}.jsonl > graph_extraction_train.jsonl
```

## Fine-Tuning

### With Unsloth (LoRA)

```python
from unsloth import FastLanguageModel
from datasets import load_dataset

dataset = load_dataset("json", data_files={
    "train": "graph_extraction_train.jsonl",
    "validation": "graph_extraction_val.jsonl",
})

# Format for Alpaca-style prompt
def format_prompt(example):
    return f"""### Instruction:
{example['instruction']}

### Input:
{example['input']}

### Response:
{example['output']}"""
```

### With OpenAI Fine-Tuning

Convert to OpenAI chat format:

```python
import json

with open("graph_extraction_train.jsonl") as f:
    for line in f:
        ex = json.loads(line)
        print(json.dumps({
            "messages": [
                {"role": "system", "content": ex["instruction"]},
                {"role": "user", "content": ex["input"]},
                {"role": "assistant", "content": ex["output"]}
            ]
        }))
```

### With Ollama Modelfile

```
FROM qwen2.5-coder:3b
ADAPTER ./graph-extraction-lora
SYSTEM "You are a knowledge graph extractor. Extract entities and relationships from developer notes. Return JSON with nodes and edges arrays."
```

## Validation

Verify JSONL integrity:

```bash
python3 -c "
import json, sys
for fname in sys.argv[1:]:
    with open(fname) as f:
        for i, line in enumerate(f, 1):
            try:
                obj = json.loads(line)
                out = json.loads(obj['output'])
                assert 'nodes' in out and 'edges' in out
            except Exception as e:
                print(f'{fname}:{i}: {e}')
    print(f'{fname}: {i} examples OK')
" graph_extraction_train_batch{1,2,3,4,5,6}.jsonl graph_extraction_val.jsonl
```
