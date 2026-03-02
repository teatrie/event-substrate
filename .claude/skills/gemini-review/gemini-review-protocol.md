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

## Caching Opportunities (Future)

Current state and future optimization paths:

- **Gemini API** supports explicit `CachedContent` objects (REST/SDK) — not yet available in Gemini CLI v0.31.0
- **Gemini CLI** has automatic within-session token caching (API key auth only, not OAuth)
- **Neither works with `ask-gemini` MCP tool** — non-interactive, fresh session per call

**Future optimization:** Build a thin API wrapper that creates a `CachedContent` object at epic start (containing `CLAUDE.md`, `architecture.md`, `avro/`), runs all reviews against it, deletes when done. Would cut per-review cost significantly for multi-review epics.

**Pragmatic workaround now:** Scope `@` references tightly per review type (e.g., `@avro/public/media/` not `@avro/`) to minimize redundant context.

**Track:** When Gemini CLI adds `/cache` command or when review costs become material, revisit.
