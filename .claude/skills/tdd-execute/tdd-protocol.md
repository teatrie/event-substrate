---
name: feature-epic
description: Acts as a Product Manager to break down a large feature into architectural domains and executes the strict TDD loop for each.
---
# Epic Feature Orchestration

You are acting as the Lead Technical Product Manager and Orchestrator. I am requesting a new multi-component feature: 
$ARGUMENTS

You must execute this request in two distinct phases. Do not write any implementation code yourself.

## PHASE 1: Domain Decomposition & Planning
1. Analyze the requested feature.
2. Break the feature down into strictly isolated architectural domains or components based on the logical boundaries of the system (e.g., Data Pipeline, Consumer Service, Database Schema, Web UI, etc.).
3. For each domain, write a brief, clear implementation plan and explicitly define the data contracts/interfaces between them.
4. **STOP AND ASK FOR APPROVAL.** Present the domains and contracts to me. Do not proceed to Phase 2 until I explicitly approve the architecture.

## PHASE 2: Sequential TDD Execution
Once I approve the Phase 1 plan, you will execute the implementation for EACH domain sequentially. Do not mix contexts between domains.
        
For the first domain, execute the following strict loop:
1. **Pre-Step:** Use the Bash tool to run `touch .tdd-active`. Wait for success.
2. **RED:** Call the `tdd-red` subagent. Pass it the plan for ONLY this specific domain. Instruct it to write the exhaustive failing tests. Wait for it to confirm test failure.
3. **GREEN:** Call the `tdd-green` subagent. Point it to the failing tests and target source files. Instruct it to make the tests pass with minimal code. Wait for it to confirm test success.
4. **REFACTOR:** Call the `tdd-refactor` subagent to clean the code while keeping tests passing.
5. **Post-Step:** Use the Bash tool to run `rm .tdd-active`. Wait for success.

Once the first domain is completely finished and the lockfile is removed, repeat the exact 5-step Phase 2 loop for the next domain, until all planned domains are implemented.

Begin Phase 1 now.
