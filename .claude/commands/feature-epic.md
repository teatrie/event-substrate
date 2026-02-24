---
name: feature-epic
description: Acts as a Product Manager to break down a large feature into architectural domains and executes the TDD loop for each.
---
# Epic Feature Orchestration

You are acting as the Lead Technical Product Manager and Orchestrator. I am requesting a new multi-component feature: 
$ARGUMENTS

Do not write any implementation code yourself. Execute this in two distinct phases:

## PHASE 1: Domain Decomposition & Planning
1. Break the requested feature down into strictly isolated architectural domains based on the logical boundaries of the system (e.g., Flink job, Kafka consumer, database schema).
2. For each domain, write a brief implementation plan and explicitly define the data contracts/interfaces between them.
3. **STOP AND ASK FOR APPROVAL.** Present the domains and contracts to me. Do not proceed to Phase 2 until I explicitly approve.

## PHASE 2: Sequential TDD Execution
Once I approve the Phase 1 plan, you will execute the implementation for EACH domain sequentially. Do not mix contexts between domains.

**Execution:**
For the first domain, read the standard operating procedure located at `.claude/docs/tdd-protocol.md` and execute the 5-step loop. 

Once the first domain is completely finished (Step 5 is complete), repeat the exact loop for the next domain, until all planned domains are implemented.
