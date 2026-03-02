---
name: check-gemini
description: Check for the latest available Gemini models and update memory + deep-research skill with current model info.
---
# Check Gemini Models Protocol

Query Gemini for the latest available models, then update memory and the `/deep-research` skill to reflect current availability.

## Step 1: Query Current Models

Ask Gemini for its own model roster:

```text
Model: gemini-3.1-flash-preview (cheap, meta-question)
Prompt: "List every Gemini model ID currently available for API use, including preview/experimental models. For each model, include: model ID string, context window size, key differentiator (speed, reasoning, cost, etc.), and whether it's GA, preview, or deprecated. Format as a markdown table sorted by capability (highest first)."
```

## Step 2: Cross-Reference

Compare the response against the current model list stored in:

1. **Memory** — `~/.claude/projects/-Users-kt-code-event-substrate/memory/MEMORY.md` → "Gemini Delegation for Deep Research" → "Available Gemini models" list
2. **Deep Research skill** — `.claude/skills/deep-research/SKILL.md` → "Step 1: Cost Analysis — Model Selection" table
3. **Gemini Review skill** — `.claude/skills/gemini-review/gemini-review-protocol.md` → model assignments per review type
4. **Gemini Direct skill** — `.claude/skills/gemini/SKILL.md` → model references and 2M context notes (verify `gemini-3.1-pro-preview` is still the 2M model)

Identify:

- **New models** not in our current list
- **Removed/deprecated models** still in our list
- **Changed context windows or status** (e.g., preview → GA)

## Step 3: Update Files

If changes are found:

1. **Update MEMORY.md** — edit the "Available Gemini models" bullet list with current model IDs, context sizes, and recommendations.
2. **Update deep-research SKILL.md** — edit the model selection table and decision heuristic to reflect new models. Preserve the cost-analysis structure.
3. **Report changes** — tell the user what changed (added, removed, updated).

If no changes: report "Models are up to date."

## Step 4: Validate

After updates, read back both files to confirm edits are correct and consistent with each other.
