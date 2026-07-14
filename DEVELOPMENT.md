# Development Guidelines

This document outlines the contribution and development practices to ensure a clean and maintainable Git history.

## 1. Commit Sign-Off Requirement

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

## 2. Linear Commit History: Rebase, Don’t Merge

We follow a **linear commit history** to keep the Git log clean and easy to follow. This means **no merge commits** should be introduced.

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

## 3. Pull Request Requirements

To maintain clarity and traceability across contributions, all pull requests must adhere to the following:

- **Description**: Every pull request must include a meaningful description outlining the purpose and scope of the change.
- **Labels**: Assign appropriate labels to help with categorization and prioritization.
- **Project Assignment**: The pull request must be added to the **Panurus** project with an appropriate status (e.g., “To Do”, “In Progress”, “In Review”, etc.).
- **Associated Issue**: A GitHub Issue must be linked to the pull request. 
  This should be done via the Development section of the GitHub PR interface (not just mentioned in the description). 
  This ensures the PR is formally connected to the issue for proper project tracking and status updates.
- **One Approve Policy**: Each PR must receive at least one `approve` from the maintainers of the project before it can be merged.
  The approver must ensure that the PR has been reviewed properly and that all CI/CD tests finish successfully. 

This ensures that all contributions are tracked, visible on the project board, and aligned with the roadmap.

## 4. Additional Notes
- Name a branch with a concise description of its purpose.
- Use clear, concise commit messages (preferably using [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/)).
- Squash commits as needed before submission to keep the history clean.
- Open a pull request only after your branch is up-to-date and rebased on `main`.
