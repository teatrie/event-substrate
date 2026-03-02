# Gemini System Architect Review Protocol

This protocol is referenced by `/gemini-review`, `/feature-epic`, `/tdd-execute`, and `/bug-fix` for independent architectural reviews via Gemini.

## Role Definition

Gemini acts as a **System Architect reviewer**, not a code reviewer. It checks:
- **Structural compliance** — domain boundaries, topic ownership, schema consistency
- **Drift detection** — conventions that have silently diverged across services
- **Global consistency** — patterns that look correct locally but conflict globally

It does NOT check:
- Logic correctness (Claude's domain)
- Test coverage (TDD protocol's domain)
- Code style (linter's domain)

**Why Gemini?** Claude reviewing Claude has correlated blind spots — all Claude models share training-family biases. Gemini is a structurally different model family with 1M-token context, ideal for global consistency checks that Claude subagents miss when working incrementally on single domains.

## Review Types

### 1. Plan Audit (CRITICAL)

**When:** `/feature-epic` Phase 1, after Claude produces domain decomposition, before presenting to user.

**Model:** `gemini-3.1-pro-preview` (architectural reasoning needs thinking mode)

**Context:**
```
@CLAUDE.md @docs/architecture.md @docs/architecture/overview.mmd @avro/ + plan text
```

**Checklist:**
- [ ] DDD boundary violations — does any domain write to another domain's topics?
- [ ] Missing data contracts — are all inter-domain interfaces defined as Avro schemas?
- [ ] Decomposition quality — are domains truly independent, or do hidden dependencies exist?
- [ ] Schema consistency — do new schemas follow `avro/{visibility}/{domain}/` conventions?
- [ ] Event naming — does every new event follow `{domain}.{entity}.{action}` pattern?
- [ ] Topic ownership — does each domain only write to its own `public.{domain}.*` topics?
- [ ] Egress compliance — do cross-domain notifications route through `user_notifications`?
- [ ] Deployment completeness — does the plan include Dockerfile, K8s manifest, Taskfile wiring for new services?
- [ ] SASL auth wiring — do new services include Kafka SASL/SCRAM-SHA-256 configuration?

**Output:** PASS/FAIL per boundary rule + findings table.

### 2. Boundary Guard (HIGH)

**When:** After GREEN checkpoint in `tdd-protocol.md`, only for cross-service changes.

**Model:** `gemini-2.5-flash` (fast structural scan)

**Context:**
```
@CLAUDE.md @avro/ @{changed files from this domain}
```

**Trigger condition:** Domain modifies files in 2+ of: `go-services/`, `flink_jobs/`, `avro/`, `kubernetes/`, `supabase/migrations/`. Single-service changes skip.

**Checklist:**
- [ ] Topic naming — new topics follow `public.{domain}.{entity}.{action}` convention
- [ ] Topic ownership — no service writes to another domain's topics
- [ ] Dependency drift — no direct DB reads across domain boundaries (only Kafka)
- [ ] Schema pattern consistency — new Avro schemas match existing patterns in `avro/`
- [ ] SASL auth wiring — new consumers/producers include SASL configuration env vars

**Output:** PASS/FAIL per structural rule.

**Skip for:** Single-service changes, config/YAML-only domains, docs.

### 3. Idiom Check (MEDIUM)

**When:** After successful REFACTOR in `tdd-protocol.md`, before POST step.

**Model:** `gemini-2.5-flash`

**Context:**
```
@{refactored files} + broader repo context for duplication scan
```

**Trigger condition:** REFACTOR actually changed code (not a no-op). Skip if refactor was skipped.

**Checklist:**
- [ ] Language idioms — does the code follow Go/Python/TypeScript conventions for this codebase?
- [ ] Codebase pattern consistency — does the new code match established patterns in the same service?
- [ ] Cross-repo duplication — does similar logic already exist elsewhere that could be reused?

**Output:** Findings list (advisory, not blocking).

**Skip for:** Skipped refactors (shipping GREEN code), trivial domains.

### 4. Bug Diagnostic Cross-Validation (LOW)

**When:** After Claude diagnostic subagent returns root cause, only for Complex-tier bugs.

**Model:** `gemini-2.5-flash-lite` (quick sanity check)

**Context:**
```
Error/symptoms + relevant code files
```

**Prompt:** Present the same error/symptoms WITHOUT Claude's diagnosis. Ask Gemini to independently identify the root cause.

**Evaluation:**
- **Agreement** (same root cause) → high confidence, proceed with fix
- **Disagreement** (different root cause) → present both analyses to user, wait for direction

**Skip for:** Simple and Medium tier bugs.

## Finding Severity

| Severity | Meaning | Action |
|----------|---------|--------|
| **PASS** | Compliant with conventions | No action needed |
| **ADVISORY** | Non-blocking suggestion | Report to user, do not block |
| **FAIL** | Structural violation | Must be addressed before proceeding |

## Disagreement Handling

When Gemini findings conflict with Claude's implementation decisions:

1. **Always report to the orchestrator/user** — never auto-fix based on Gemini's suggestion
2. **Present both perspectives** — Claude's rationale and Gemini's concern
3. **Let the user decide** — the human architect has final authority
4. **Document the decision** — if the user overrides a FAIL, note it in the domain commit message

Gemini is a second opinion, not an authority. Its structural checks catch real issues, but it lacks the incremental context that Claude's TDD loop provides.

## Caching Strategy

Two paths coexist for Gemini reviews:

**One-off reviews:** Use `ask-gemini` MCP (OAuth, free). Default for single reviews outside an epic context.

**Multi-review epics (3+ reviews sharing context):** Use `gemini-api.py` with explicit caching for 90% cost reduction:

1. **Orchestrator creates cache at epic start:**
   ```bash
   uv run .claude/scripts/gemini-api.py cache create \
     --model gemini-2.5-flash \
     --files CLAUDE.md docs/architecture.md docs/architecture/overview.mmd avro/ \
     --name "epic-{feature}" --ttl 7200
   ```

2. **All review steps query against the cache:**
   ```bash
   uv run .claude/scripts/gemini-api.py query \
     --model gemini-2.5-flash \
     --cache "cachedContents/abc123" \
     --prompt "Review type: Boundary Guard ..."
   ```

3. **Orchestrator deletes cache when epic completes:**
   ```bash
   uv run .claude/scripts/gemini-api.py cache delete cachedContents/abc123
   ```

**Cache is model-locked** — if the epic mixes models (e.g., `gemini-3.1-pro-preview` for Plan Audit, `gemini-2.5-flash` for Boundary Guard), create separate caches per model.

**>1M context reviews:** Use `gemini-api.py query --model gemini-3.1-pro-preview` (the only model with 2M context). The `ask-gemini` MCP (OAuth) caps at 1M.
