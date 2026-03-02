---
name: deep-research
description: Delegate deep research and analysis to Gemini's large context window. Runs cost analysis to select the cheapest effective model, then passes the task via the ask-gemini MCP tool.
---
# Deep Research Protocol

Delegate research-heavy and analysis-heavy tasks to Gemini via the `ask-gemini` MCP tool. Gemini models have up to 1M token context windows — far larger than Claude's — making them superior for tasks that require reading large amounts of code or text at once.

Run `/check-gemini` periodically to refresh the model list below.

## When to Use

- Deep codebase analysis (entire directories, repos, or large modules)
- Large file processing (logs, traces, data dumps, long documents)
- Cross-file pattern analysis (finding conventions, inconsistencies, dependencies)
- Architecture reviews of unfamiliar or large codebases
- Git history analysis (large diffs, blame across many files)
- Any research-only task where breadth of context matters more than tool use

## When NOT to Use

- Tasks requiring file edits, writes, or bash execution (Gemini MCP is read-only analysis)
- Simple directed searches (use Grep/Glob directly — faster and cheaper)
- Tasks requiring fewer than ~5 file reads (Claude handles these fine)
- Tasks where Claude's tool use (Edit, Bash, etc.) is the bottleneck, not context

---

## Step 1: Cost Analysis — Model Selection

Before every `ask-gemini` call, assess the task and select the cheapest effective model:

*Last verified: 2026-03-02*

| Tier | Model | Context | Status | Use When |
|------|-------|---------|--------|----------|
| Cheapest | `gemini-2.5-flash-lite` | 1M | Stable GA | Simple lookups, quick scans, meta-questions. Fastest and cheapest. |
| Default | `gemini-2.5-flash` | 1M | Stable GA | **Default choice.** Broad scans, pattern searches, codebase overviews, straightforward analysis. Best price-performance. |
| Fast frontier | `gemini-3-flash-preview` | 1M | Preview | Frontier reasoning at flash speed. Use when 2.5-flash output is shallow or task needs stronger reasoning. |
| Deep reasoning | `gemini-3.1-pro-preview` | 1M | Preview | Deep architectural analysis, complex cross-file reasoning, subtle bug hunting. Top-tier reasoning with thinking mode. |
| Stable fallback | `gemini-2.5-pro` | 1M | Stable GA | Fallback if preview models return errors or degraded output. Stable deep reasoning. |

**Non-existent models (DO NOT USE):** `gemini-3.1-flash-preview`, `gemini-3-deep-think`, `gemini-3-flash` — all return 404.

**Decision heuristic:**

1. **Start with `gemini-2.5-flash`.** It handles 80%+ of research tasks at the lowest cost with stable GA quality.
2. **Escalate to `gemini-3-flash-preview`** if: 2.5-flash output is shallow, or task needs frontier-class reasoning at speed.
3. **Escalate to `gemini-3.1-pro-preview`** if: task involves >50 files, requires subtle architectural reasoning, or lower tiers produce incomplete analysis.
4. **Fall back to `gemini-2.5-pro`** if preview models are unavailable or returning errors.

---

## 2M Mega-Context Mode

If the research task requires >1M tokens of context (e.g., full repo analysis), use the API key path instead of `ask-gemini`:

```bash
uv run .claude/scripts/gemini-api.py query \
  --model gemini-3.1-pro-preview \
  --files . \
  --prompt "your research question"
```

Only `gemini-3.1-pro-preview` supports 2M context (2,097,152 tokens). Requires `GEMINI_API_KEY` env var (free from [Google AI Studio](https://aistudio.google.com)).

For multi-query research sessions, create a cache first to get 90% cost reduction on subsequent queries:

```bash
uv run .claude/scripts/gemini-api.py cache create \
  --model gemini-3.1-pro-preview --files . --name "research-session" --ttl 3600
uv run .claude/scripts/gemini-api.py query \
  --model gemini-3.1-pro-preview --cache "cachedContents/abc123" --prompt "question 1"
uv run .claude/scripts/gemini-api.py query \
  --model gemini-3.1-pro-preview --cache "cachedContents/abc123" --prompt "question 2"
uv run .claude/scripts/gemini-api.py cache delete cachedContents/abc123
```

For ≤1M context, keep using `ask-gemini` MCP (OAuth, free) as the default path below.

---

## Step 2: Construct the Prompt

Structure the Gemini prompt for maximum utility:

```text
@<path> [additional @paths if needed]

<TASK>
[Clear description of what to research/analyze]

<FOCUS>
[Specific questions to answer or patterns to look for]

<OUTPUT FORMAT>
[How to structure the response — e.g., "bullet list of findings", "table comparing approaches", "ranked list with rationale"]
```

**Key rules:**

- Use `@` references to include files/directories — this is how Gemini ingests context.
- Be specific about what you want back. Vague prompts waste tokens.
- Request structured output (tables, lists, sections) so results are easy to consume.
- If the codebase is very large, scope `@` references to relevant subdirectories rather than `@./` (the entire repo).

---

## Step 3: Execute and Handle Response

1. Call `ask-gemini` with the constructed prompt and the selected `model` parameter.
2. If the response mentions chunking (partial response with a `cacheKey`), use `fetch-chunk` to retrieve remaining chunks.
3. **Validate the results** (see Step 4).
4. Summarize validated findings back to the user or into your working context. Do NOT dump raw Gemini output — distill it.

---

## Step 4: Validate Results

Gemini can hallucinate file paths, function names, config values, and architectural claims. **Never trust Gemini output without verification.** Before presenting results to the user or acting on them:

1. **Spot-check file references.** For every file path Gemini mentions, verify it exists using Glob. If Gemini references >10 files, spot-check at least 3-5 representative ones.
2. **Spot-check code claims.** If Gemini claims a function exists, a variable has a certain value, or code follows a pattern — use Grep or Read to verify at least the key claims. Prioritize claims that would influence decisions.
3. **Cross-reference names.** Verify function names, type names, topic names, config keys, and env vars against the actual codebase. Gemini frequently gets these subtly wrong (e.g., `handleUpload` vs `HandleUpload`, `credit_balance` vs `credit_ledger`).
4. **Flag unverifiable claims.** If Gemini makes architectural assertions you cannot quickly verify (e.g., "this is thread-safe", "this handles backpressure"), mark them as **unverified** when presenting to the user.
5. **Reject fabricated content.** If spot-checks reveal >2 hallucinated references (files that don't exist, functions that aren't real), discard the entire response and either re-run with a more specific prompt or escalate to a higher-tier model.

**Validation depth by task type:**

- **Codebase scan** (topic lists, dependency maps) → verify all file paths, spot-check 50%+ of specific claims
- **Architecture review** → verify all component names and data flow connections, flag reasoning as unverified
- **Pattern search** → verify every reported match exists

---

## Step 5: Escalation

If the selected model's output is:

- **Shallow or incomplete** → Re-run with the next tier up (`gemini-2.5-flash` → `gemini-3-flash-preview` → `gemini-3.1-pro-preview`).
- **Erroring or unavailable** → Fall back to `gemini-2.5-pro` (stable GA).
- **Still insufficient after Pro** → Report findings to the user and suggest a manual review approach.

---

## Examples

### Broad codebase scan

```text
Model: gemini-2.5-flash (broad scan, straightforward analysis)

@go-services/ @flink_jobs/

<TASK>
Identify all Kafka topic references across Go services and Flink SQL jobs.

<FOCUS>
- Which topics are produced to vs consumed from in each service?
- Are there any topics referenced in code but not defined in docker-compose or scripts?
- Any naming convention violations (should be public.{domain}.{entity}.{action})?

<OUTPUT FORMAT>
Table: | Service | Topic | Direction (produce/consume) | Convention OK? |
```

### Deep architectural analysis

```text
Model: gemini-3.1-pro-preview (complex cross-file reasoning)

@go-services/ @flink_jobs/ @supabase/migrations/ @avro/

<TASK>
Trace the complete data flow for the media upload saga from API gateway through Kafka, Flink processing, and database writes.

<FOCUS>
- Identify every state transition and which component owns it
- Find any gaps where events could be lost (no retry, no DLQ)
- Assess whether the saga follows the domain boundary conventions in CLAUDE.md

<OUTPUT FORMAT>
1. Ordered sequence of steps with component ownership
2. Gap analysis table: | Gap | Risk | Recommendation |
3. Domain boundary assessment (pass/fail per boundary rule)
```

### Algorithm / concurrency analysis

```text
Model: gemini-3.1-pro-preview (complex logical reasoning)

@go-services/consumer/internal/credits/balance.go

<TASK>
Analyze the credit balance check-and-deduct logic for correctness under concurrent access.

<FOCUS>
- Can two concurrent requests both pass the balance check and cause a negative balance?
- Is the current locking strategy sufficient?
- What are the formal invariants that must hold?

<OUTPUT FORMAT>
1. Concurrency analysis with specific race condition scenarios
2. Formal invariants list
3. Fix recommendations ranked by correctness guarantee
```
