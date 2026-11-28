# Best Practices for Versioning Go Projects

Effective versioning is crucial for managing dependencies, ensuring stability, and communicating changes in your Go projects. 
With the introduction of Go Modules, Go has a canonical way to handle versions. 
This guide outlines the best practices to follow.

## 1. Embrace Go Modules

Go Modules are the official dependency management system for Go and are central to versioning. 
If you're starting a new project or maintaining an existing one, ensure it's using Go Modules.

-   **`go.mod` file:** This file, located at the root of your module, defines the module's path (its unique identifier), the Go version it's built with, and its dependencies with their specific versions.
-   **Initialization:** Start using modules in your project with `go mod init <module_path>`, for example, `go mod init github.com/yourusername/yourproject`.

## 2. Adhere to Semantic Versioning (SemVer)

Go modules use [Semantic Versioning (SemVer) 2.0.0](https://semver.org/). Version numbers are typically in the format `vMAJOR.MINOR.PATCH`.

-   **`MAJOR` version (e.g., `v1.0.0` -> `v2.0.0`):** Incremented when you make incompatible API changes.
-   **`MINOR` version (e.g., `v1.0.0` -> `v1.1.0`):** Incremented when you add functionality in a backward-compatible manner.
-   **`PATCH` version (e.g., `v1.0.0` -> `v1.0.1`):** Incremented when you make backward-compatible bug fixes.

**Key SemVer Points in Go:**

-   **`v0.y.z` (Initial Development):** Major version zero is for initial development. Anything may change at any time. The public API should not be considered stable.
-   **`v1.0.0` (First Stable Release):** This version defines the first stable public API. From this point on, changes are governed by SemVer rules regarding backward compatibility.
-   **Pre-release Versions:** You can denote pre-releases by appending a hyphen and a series of dot-separated identifiers (e.g., `v1.0.0-alpha`, `v2.1.0-beta.1`). Pre-releases have lower precedence than their normal version (e.g., `v1.0.0-alpha` comes before `v1.0.0`).
-   **Build Metadata:** You can append build metadata with a plus sign (e.g., `v1.0.0+build.123`). Build metadata is ignored when determining version precedence.

## 3. Use Git Tags for Releases

When you release a new version of your module, you should create a Git tag with the semantic version number.

-   **Tagging:**
    ```bash
    git tag v1.0.1
    git push origin v1.0.1
    ```
-   **Immutable Versions:** Once a version is tagged and published, it should be considered immutable. Any changes must be released as a new version.

## 4. Managing Major Versions (v2 and Beyond)

This is a critical aspect of Go versioning due to the "import compatibility rule." 
When you release a `v2` or higher major version (which implies breaking changes):

-   **Module Path Must Change:** The module path in your `go.mod` file **must** be updated to include the major version suffix. 
    For example, if your module was `github.com/yourusername/yourproject` for `v0` and `v1` versions, for `v2.0.0` and subsequent `v2.x.x` versions, the module path becomes `github.com/yourusername/yourproject/v2`.
    ```
    // go.mod for v1.x.x
    module [github.com/yourusername/yourproject](https://github.com/yourusername/yourproject)
    go 1.21

    // go.mod for v2.x.x
    module [github.com/yourusername/yourproject/v2](https://github.com/yourusername/yourproject/v2)
    go 1.21
    ```
-   **Import Path Updates:** Consumers of your module will need to update their import paths in their Go source files to use the new major version.
    ```go
    // Importing v1
    import "[github.com/yourusername/yourproject/somepackage](https://github.com/yourusername/yourproject/somepackage)"

    // Importing v2
    import "[github.com/yourusername/yourproject/v2/somepackage](https://github.com/yourusername/yourproject/v2/somepackage)"
    ```
-   **A New Module:** Effectively, a new major version (`v2`, `v3`, etc.) is treated as a new, separate module by the Go toolchain.

**Strategies for Structuring Major Versions in Your Repository:**

1.  **Major Version Subdirectory (Recommended by Go Team):**
    -   Create a new directory in your repository for the new major version (e.g., `v2/`, `v3/`).
    -   Copy the `v1` codebase into this new directory as a starting point.
    -   The `go.mod` file within this subdirectory will declare the `/v2` module path (e.g., `module github.com/yourusername/yourproject/v2`).
    -   The `v0`/`v1` code can remain in the root directory or its own `v1/` directory.
    -   Tags for `v2` releases would be like `v2.0.0`, `v2.0.1`, etc. (If the `v2` code is in a `v2/` subdirectory, the Go toolchain understands this. Some projects tag like `v2/v2.0.0` but this is less common now).
    -   *Pros:* Compatible with older Go versions (pre-Go 1.11 GOPATH mode), allows concurrent development of multiple major versions.

2.  **Major Version Branch:**
    -   Create a new Git branch for the new major version (e.g., `v2-branch`).
    -   In this branch, update the `go.mod` file to include the major version suffix in the module path.
    -   Tag releases from this branch (e.g., `v2.0.0`).
    -   *Pros:* Clearer separation in version control, cleaner repository structure without duplicated directories in the main branch.
    -   *Cons:* Users might need to be more aware of which branch to track for a specific major version if they are not just relying on `go get`.

**Considerations for Major Version Updates:**

-   **Disruption:** Major version updates can be disruptive for your users. Only make them when absolutely necessary.
-   **Support:** Clearly communicate your support policy for previous major versions.
-   **Maintenance:** Be prepared to maintain multiple major versions if necessary (e.g., backporting security fixes).

## 5. Pseudo-Versions

When you `go get` a specific commit hash or a branch name that hasn't been tagged with a semantic version, Go tools will generate a "pseudo-version" in your `go.mod` file.

-   **Format:** `vX.Y.Z-yyyymmddhhmmss-commitabbrev` (e.g., `v0.0.0-20231027143000-abc123def456`).
    -   `vX.Y.Z-0`: Base version from a preceding tag, or `vX.0.0` if no tag.
    -   `yyyymmddhhmmss`: UTC timestamp of the commit.
    -   `commitabbrev`: 12-character prefix of the commit hash.
-   **Usage:** Useful for development or when you need a specific fix that isn't yet in a tagged release. However, for stable dependencies, always prefer tagged semantic versions.
-   **Avoid Manual Creation:** Let Go tools generate pseudo-versions.

## 6. Publishing Your Module

Go modules are typically published by pushing tags to a version control repository (e.g., GitHub, GitLab).

1.  Commit your changes.
2.  Tag the commit: `git tag vX.Y.Z`
3.  Push the tag: `git push origin vX.Y.Z`

The Go proxy (and other users) will then be able to discover and download this version.

## 7. Multi-Module Repositories

While the common practice is one module per repository, Go does support multiple modules within a single repository.

-   **Tag Prefixes:** If you have modules in subdirectories (e.g., `repo/modA/go.mod` and `repo/modB/go.mod`), you need to prefix your version tags with the directory name:
    -   For `modA`: `git tag modA/v1.0.0`
    -   For `modB`: `git tag modB/v1.2.0`
-   **Complexity:** This can add complexity to your build and release process. Consider carefully if this structure is necessary.

### Internal requires

Panurus has several modules. When one requires another:

-   Require the last published release of that module, for example `github.com/LFDT-Panurus/panurus v0.28.0` or `github.com/LFDT-Panurus/panurus/integration v0.27.0`.
-   Keep the local `replace` directive. Builds on `main` use the local code.
-   Never require `v0.0.0`, `v0.0.0-00010101000000-...` or an unpublished version. A tag publishes the `go.mod` as it is, and consumers cannot resolve these versions.
-   Do not add a `toolchain` line.

`make gomod-requires-check` (part of `make checks-fast`) enforces the placeholder and `toolchain` rules.

## 8. General Git Best Practices

While not specific to Go versioning, good Git hygiene supports a clean versioning strategy:

-   **Commit Small, Logical Changes:** Makes history easier to understand and revert if needed.
-   **Write Meaningful Commit Messages:** Follow conventions like Conventional Commits to clearly explain changes. This can also help automate changelog generation.
-   **Use Branches:** For features (`feature/my-new-feature`), bug fixes (`fix/that-annoying-bug`), etc.
-   **Pull Before You Push:** Keep your local repository up-to-date to avoid merge conflicts.
-   **Use Pull Requests (PRs) / Merge Requests (MRs):** Facilitate code review and discussion before merging changes.

## Conclusion

By following these best practices, particularly adhering to Semantic Versioning and understanding how Go Modules handle major versions, you can create a robust and predictable versioning scheme for your Go projects. 
This benefits both you as the maintainer and the users of your module.
