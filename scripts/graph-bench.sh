#!/bin/bash
# Graph extraction benchmark — compare models on the eval vault
# Run from the statelessagent repo root: ./scripts/graph-bench.sh

set -e

SAME="./build/same"
VAULT="eval/test_vault"
CONFIG="$VAULT/.same/config.toml"
RESULTS="graph-bench-results.txt"

# Models to test (smallest to largest)
MODELS=("qwen2.5:1.5b" "llama3.2" "qwen2.5:7b" "qwen3:8b")

# Check prerequisites
if [ ! -f "$SAME" ]; then
  echo "Building same..."
  make build
fi

if ! command -v ollama &>/dev/null; then
  echo "Error: ollama not found"
  exit 1
fi

echo "Graph Extraction Benchmark" | tee "$RESULTS"
echo "=========================" | tee -a "$RESULTS"
echo "Date: $(date)" | tee -a "$RESULTS"
echo "Vault: $VAULT" | tee -a "$RESULTS"
echo "" | tee -a "$RESULTS"

# Count notes in vault
NOTE_COUNT=$(find "$VAULT" -name "*.md" -not -path "*/.same/*" | wc -l | tr -d ' ')
echo "Notes in vault: $NOTE_COUNT" | tee -a "$RESULTS"
echo "" | tee -a "$RESULTS"

# Make sure graph LLM is enabled
if grep -q 'llm_mode = "off"' "$CONFIG" 2>/dev/null; then
  sed -i '' 's/llm_mode = "off"/llm_mode = "on"/' "$CONFIG"
  echo "Enabled graph LLM mode in config"
fi

# Ensure config has a model line under [graph]
if ! grep -q '^model = ' "$CONFIG" 2>/dev/null; then
  sed -i '' '/^\[graph\]/a\
model = "qwen2.5:1.5b"
' "$CONFIG"
fi

for model in "${MODELS[@]}"; do
  echo "-------------------------------------------" | tee -a "$RESULTS"
  echo "Model: $model" | tee -a "$RESULTS"
  echo "-------------------------------------------" | tee -a "$RESULTS"

  # Check model is available
  if ! ollama list | grep -q "$(echo $model | cut -d: -f1)"; then
    echo "  SKIP: model not installed (run: ollama pull $model)" | tee -a "$RESULTS"
    echo "" | tee -a "$RESULTS"
    continue
  fi

  # Update config to use this model
  sed -i '' "s/^model = .*/model = \"$model\"/" "$CONFIG"

  # Rebuild graph and time it
  echo "  Rebuilding graph..." | tee -a "$RESULTS"
  START=$(date +%s)
  REBUILD_OUTPUT=$($SAME graph rebuild --vault "$VAULT" 2>&1)
  END=$(date +%s)
  DURATION=$((END - START))

  echo "  Time: ${DURATION}s" | tee -a "$RESULTS"

  # Get stats
  STATS=$($SAME graph stats --vault "$VAULT" 2>&1)
  echo "$STATS" | tee -a "$RESULTS"

  # Extract key numbers
  NODES=$(echo "$STATS" | grep -i "node" | head -1 | grep -oE '[0-9]+' | head -1)
  EDGES=$(echo "$STATS" | grep -i "edge" | head -1 | grep -oE '[0-9]+' | head -1)

  echo "  Summary: ${NODES:-0} nodes, ${EDGES:-0} edges in ${DURATION}s" | tee -a "$RESULTS"
  echo "" | tee -a "$RESULTS"
done

echo "==========================================" | tee -a "$RESULTS"
echo "Benchmark complete. Results saved to $RESULTS" | tee -a "$RESULTS"
echo ""
echo "To view: cat $RESULTS"
