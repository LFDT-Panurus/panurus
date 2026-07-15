# Development Guidelines

This document outlines the contribution and development practices to ensure a clean and maintainable Git history.

## Creating a Branch

When checking out a new branch from `main` to work on an issue, give it a name associated with the issue it is addressing
For example branch name: `123-user-management`.

Work on your local branch and make as many local commits as needed.

## Commit Sign-Off Requirement

All commits must be **signed off** to certify that the contributor agrees to the [Developer Certificate of Origin (DCO)](https://developercertificate.org/).  
This is a legal statement indicating that you wrote the code or have the right to contribute it.

### How to Sign Off a Commit

When creating a commit, use the `-s` flag:

```bash
git commit -s -m "feat: add token validation logic"
```

This will append a `Signed-off-by` line with your name and email address, matching the information in your Git configuration.

Example:

```
Signed-off-by: Jane Doe <jane.doe@example.com>
```

If you forgot to sign off a commit, you can amend the last commit:

```bash
git commit --amend -s
```

Or for multiple commits, you can rebase and add the sign-offs:

```bash
git rebase -i HEAD~N  # replace N with number of commits to edit
# Then mark each commit with 'edit' and sign off each one:
git commit --amend -s
git rebase --continue
```

## Ensure Linear Commit History

We follow a linear commit history to keep the Git log clean and easy to follow. 
We should have one compound commit per feature/issue to make it easy to track the project history.
This involves the following steps:
- Iteratively develop in your branch adding as many intermediate commits as needed.
- Before merging, rebase your branch to the latest main to avoid merge conflicts.
- Make a pull request and iterate on comments.
- Once approved, squash and merge your PR.
### Rebase Workflow

1. Before pushing your branch, always rebase on the latest `main`:

   ```bash
   git fetch origin
   git rebase origin/main
   ```

2. If there are any conflicts, resolve them and continue the rebase:

   ```bash
   git status           # See conflicted files
   # Edit and resolve conflicts
   git add <resolved-files>
   git rebase --continue
   ```

3. Force push your rebased branch to update your pull request:

   ```bash
   git push --force-with-lease
   ```

### Why No Merge Commits?

- They clutter the history.
- They make it harder to identify what changes were made.
- They complicate tools that generate changelogs or audit logs.


### Pull Request Requirements

When ready to make a `Pull request`, do the following:
- Open the PR in Github
- Create a title reflecting the task and `issue` number
  - Example Title: `Add user management (#123)`
  - The #123 will become a clickable link to the `issue`, and will also show up in the commit history
- Create additional text as needed
    - Example Text:
       ```
       Author: Jens Jelitto
       Reviewers: Bar, Angelo
       Issue: /link (if issue is attached)
       Description: any additional content description
       ```  

### Finalize Pull Request

Once the review process is finished, finalize the PR:
- Do a `squash and merge` (NOT `Merge commit`!)
- Delete the branch
- Close the PR

To maintain clarity and traceability across contributions, all pull requests must adhere to the following:

- **Description**: Every pull request must include a meaningful description outlining the purpose and scope of the change: what the problem was, and how it was addressed.
- **Assignee**: Assign the PR to whoever is doing the work (yourself, if you opened it).
- **Labels**: Assign appropriate labels to help with categorization and prioritization. Check `gh label list` for the current set rather than inventing new ones.
- **Milestone**: Set the milestone the work is targeted for (see `gh api repos/LFDT-Panurus/panurus/milestones --jq '.[].title'` for the current list).
- **Project Assignment**: The pull request must be added to the **Panurus** project with an appropriate status (e.g., “To Do”, “In Progress”, “In Review”, etc.).
  The maintainers are responsible to set this appropriately. If the creator of the PR is a maintainer, then they will also set this.    
- **Associated Issue**: A GitHub Issue must be linked to the pull request.
  This should be done via the Development section of the GitHub PR interface (not just mentioned in the description) — e.g. include `Fixes #123` or `Closes #123` in the PR body, which GitHub links automatically on creation.
  This ensures the PR is formally connected to the issue for proper project tracking and status updates.
- **One Approve Policy**: Each PR must receive at least one `approve` from the maintainers of the project before it can be merged.

This ensures that all contributions are tracked, visible on the project board, and aligned with the roadmap.

Using the `gh` CLI, assignee/labels/milestone/project can all be set in a single `gh pr create` call:

```bash
gh pr create --title "Add user management (#123)" --base main \
  --assignee @me \
  --label "bug,hardening" \
  --milestone "Q3/26" \
  --project "Panurus" \
  --body "Fixes #123

  <description of the problem and how it was addressed>"
```

Note: GitHub's Issue Type field (Bug/Task/Feature) does **not** exist on pull requests — only on issues (see below). There is nothing to set on the PR for it.

## Issue Creation Guidelines

Before starting non-trivial work (a bug fix or a new feature), open a GitHub Issue describing the problem or the request — unless one already exists. The issue documents the *problem*, independent of whatever fix eventually lands; do not write it as if the fix is already known/in progress.

A well-formed issue includes:

- **Summary**: what's wrong (or what's needed) and where.
- **Impact**: why it matters — data loss, silent failure, blocked workflow, etc.
- **Suggested fix direction** (optional): pointers for whoever picks it up, without prescribing the exact implementation.

Populate the same metadata fields as PRs — **Assignee**, **Labels**, **Milestone**, **Project** (`Panurus`) — plus one PR-issues don't have:

- **Issue Type** (Bug / Task / Feature): a GitHub-native field, distinct from labels. As of this writing, the `gh` CLI (`gh issue create` / `gh issue edit`) has **no flag** for it — it must be set via the GraphQL API:

  ```bash
  # 1. Find the org's issue-type node IDs (one-time lookup, or if they change):
  gh api orgs/LFDT-Panurus/issue-types --jq '.[] | {name, node_id}'

  # 2. Get the issue's node ID:
  gh api repos/LFDT-Panurus/panurus/issues/<N> --jq '.node_id'

  # 3. Set the type:
  gh api graphql -f query='
    mutation {
      updateIssueIssueType(input: {issueId: "<issue node_id>", issueTypeId: "<type node_id>"}) {
        issue { id }
      }
    }'
  ```

Example creating a fully-populated issue in one command (Issue Type still requires the GraphQL follow-up above):

```bash
gh issue create --repo LFDT-Panurus/panurus \
  --title "ttx: Transaction.appendPayload overwrites TokenRequest/Transient instead of merging" \
  --body "<summary, impact, suggested fix direction>" \
  --assignee @me \
  --label "bug,ttx,hardening,testing" \
  --milestone "Q3/26" \
  --project "Panurus"
```

## Epic Creation Guidelines

For larger features or workstreams, create a **GitHub Epic** to group related issues and provide overarching context. Each epic should include:

- **A clear description** outlining the goal or deliverable of the epic.
- **A checklist** referencing all related GitHub Issues that the epic encompasses. This provides a roadmap for tracking progress and dependencies.

Don't forget to populate the metadata (like Project, Milestone, and so on) for both epics, issues, and PRs.

Example Epic Template:

```markdown
## Objective
This epic tracks the implementation of <feature/initiative name>, including all tasks required for completion.

## Task List
- #101
- #102
- #103
```
Github will replace `#101` with the title of corresponding Github Issue and report the status as well.


Organizing work in epics ensures better visibility for contributors and maintainers, and helps align development with roadmap goals.

## Additional Notes

- Use clear, concise commit messages (preferably using [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/)).
- Squash commits as needed before submission to keep the history clean.
- Open a pull request only after your branch is up-to-date and rebased on `main`.