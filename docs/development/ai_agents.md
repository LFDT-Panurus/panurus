# AI Agents Best Practices

Panurus project leverages AI agents to streamline development, maintenance, and testing. 
To ensure consistent and high-quality results when using AI agents, please follow the guidelines below.

## Agent Context (`AGENTS.md`)

The [AGENTS.md](../../AGENTS.md) file in the root directory is the **primary source of truth** for AI agents. 
It provides a comprehensive overview of the project's architecture, key components, building instructions, and development conventions.

When starting a session with an AI agent, ensure it has read this file to understand the project's specific context.

## Best Practices for AI-Assisted Development

### 1. Research and Strategy Before Execution
Before asking an agent to implement a feature or fix a bug, ensure it performs a research phase.
- **Goal:** Understand existing patterns and dependencies.
- **Action:** Use tools like `grep_search`, `glob`, and `read_file` to map the codebase.
- **Verification:** Always verify assumptions by reading the actual source code.

### 2. Empirical Bug Reproduction
Never apply a fix based on an observation alone.
- **Goal:** Confirm the failure state and prevent regressions.
- **Action:** Ask the agent to create a reproduction script or a new test case that fails before implementing the fix.

### 3. Idiomatic and Consistent Code
The agent must adhere to the project's Go coding standards.
- **Goal:** Maintain a seamless and maintainable codebase.
- **Action:** Reference [Writing idiomatic, effective, and clean Go code](./idiomatic.md) and ensure the agent uses `make lint-auto-fix` after making changes.

### 4. Comprehensive Testing and Validation
Validation is the only path to finality.
- **Goal:** Ensure correctness and prevent regressions.
- **Action:** Every change must include a testing strategy. For new features, this means adding unit tests or integration tests. For bug fixes, it means verifying the fix with the reproduction case.
- **Mandate:** Always run `make unit-tests` and relevant integration tests (e.g., `make integration-tests-fabtoken-fabric-t1`).

### 5. Surgical and Atomic Changes
Keep changes focused and minimal.
- **Goal:** Reduce complexity and make reviews easier.
- **Action:** Instruct the agent to perform surgical updates rather than broad refactorings, unless specifically requested.

## Common Agent Workflows

- **Bug Fixing:** Research -> Reproduce -> Strategy -> Fix -> Validate.
- **Feature Addition:** Research -> Design -> Strategy -> Implement -> Test -> Validate.
- **Documentation:** Research -> Draft -> Review -> Refine.

## Feedback and Iteration
If an agent provides suboptimal results, provide specific feedback based on the project's conventions. 
Update `AGENTS.md` if there are persistent misunderstandings about the project's architecture or standards.
