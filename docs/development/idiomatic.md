# Writing idiomatic, effective, and clean Go code 

Writing idiomatic, effective, and clean Go code involves adhering to a set of principles and practices that leverage the language's unique design. 
Here are some key guidelines, often considered "commandments" in the Go community:

**The Ten Commandments of Idiomatic Go:**

1.  **Thou Shalt Format Thy Code with `golangci-lint`:**

    * **Guideline:** Use the `golangci-lint` tool religiously. It enforces a standard, opinionated style for indentation, spacing, and alignment.
    We support the tool directly in our `Makefile`. You can run:
    ```bash
    make lint
    ```
    and 
    ```bash
    make checks
    ```
    * This consistency makes Go code highly readable and reduces time spent on style debates. Many editors and IDEs integrate `gofmt` automatically on save. The `goimports` tool is a superset of `gofmt` that also manages imports.
    * **References:**
        * [Effective Go - Formatting](https://www.google.com/search?q=https://go.dev/doc/effective_go%23formatting)
        * [Go standards and style guidelines - GitLab Docs](https://docs.gitlab.com/development/go_guide/) (Mentions `goimports`)
        * [golangci-lint Fast linters runner for Go](https://github.com/golangci/golangci-lint)

2.  **Thou Shalt Handle Errors Explicitly:**

    * **Guideline:** Go treats errors as values. Functions that can fail should return an error as the last return value. 
    Always check error returns and handle them gracefully. Avoid ignoring errors using the blank identifier `_`. 
    Propagate errors back up the call stack or handle them appropriately (e.g., logging, retrying). 
    Use `errors.Is` and `errors.As` for checking error types or values, especially in Go 1.13+. Error strings should be lowercase and not end with punctuation.
    * **References:**
        * [Effective Go - Errors](https://www.google.com/search?q=https://go.dev/doc/effective_go%23errors)
        * [Golang 10 Best Practices - Proper Error Handling](https://codefinity.com/blog/Golang-10-Best-Practices)
        * [Go standards and style guidelines - GitLab Docs](https://docs.gitlab.com/development/go_guide/) (Mentions `errors.Is` and `errors.As`)

3.  **Thou Shalt Favor Composition Over Inheritance:**

    * **Guideline:** Go does not have traditional class inheritance. 
    Achieve code reuse and flexibility through composition (embedding structs) and interfaces. 
    Design small, focused interfaces that define behavior.
    * **References:**
        * [Idiomatic Design Patterns in Go - Composition](https://ayada.dev/go-roadmap/idiomatic-design-patterns-in-go/)

4.  **Thou Shalt Design Small, Focused Interfaces:**

    * **Guideline:** Go's interfaces are implicitly implemented. 
    Define interfaces on the consumer side, specifying only the methods a client needs. 
    This promotes decoupling and testability. 
    Name interfaces with an "-er" suffix (e.g., `Reader`, `Writer`) when they define a single method, though this is a convention, not a strict rule for all interfaces.
    * **References:**
        * [Effective Go - Interfaces](https://www.google.com/search?q=https://go.dev/doc/effective_go%23interfaces)
        * [Golang style guide - Mattermost Developers](https://www.google.com/search?q=https://developers.mattermattermost.com/contribute/more-info/server/style-guide/%23interfaces) (Mentions the "-er" convention)

5.  **Thou Shalt Write Concurrent Code Using Goroutines and Channels:**

    * **Guideline:** Embrace Go's built-in concurrency primitives. 
    Use goroutines for concurrent execution and channels for safe communication and synchronization between them. 
    Understand the Go memory model and use tools like the race detector to avoid race conditions.
    * **References:**
        * [Effective Go - Concurrency](https://www.google.com/search?q=https://go.dev/doc/effective_go%23concurrency)
        * [Golang 10 Best Practices - Efficient Use of Goroutines](https://codefinity.com/blog/Golang-10-Best-Practices)
        * [Idiomatic Design Patterns in Go - Concurrency with Goroutines and Channels](https://ayada.dev/go-roadmap/idiomatic-design-patterns-in-go/)

6.  **Thou Shalt Not Use Global Variables Extensively:**

    * **Guideline:** Limit the use of global variables to avoid side effects and improve testability and maintainability. 
    Pass data explicitly through function parameters and return values, or use struct fields.
    * **References:**
        * [Golang 10 Best Practices - Minimize Global Variables](https://codefinity.com/blog/Golang-10-Best-Practices)
        * [Go standards and style guidelines - GitLab Docs](https://docs.gitlab.com/development/go_guide/) (Avoid global variables)

7.  **Thou Shalt Keep Functions Small and Single-Purpose:**

    * **Guideline:** Design functions that do one thing well. 
    Short, focused functions are easier to understand, test, and maintain. 
    Avoid excessive nesting and complex logic within a single function.
    * **References:**
        * [Golang 10 Best Practices - Keep Functions Focused](https://codefinity.com/blog/Golang-10-Best-Practices)
        * [Golang Clean Code Guide – 2 - Simplicity in Action: Short and Focused Functions](https://web.archive.org/web/20250916174054/https://withcodeexample.com/golang-clean-code-guide-2/)

8.  **Thou Shalt Write Tests:**

    * **Guideline:** Go has a built-in testing framework. 
    Write unit tests for your code to ensure correctness and provide examples of how to use your functions and types. 
    Table-driven tests are a common and effective pattern in Go.
    * **References:**
        * [Effective Go - Testing](https://www.google.com/search?q=https://go.dev/doc/effective_go%23testing)
        * [Go standards and style guidelines - GitLab Docs](https://docs.gitlab.com/development/go_guide/) (Defining test cases)

9.  **Thou Shalt Document Exported Symbols:**

    * **Guideline:** Provide clear and concise documentation for all exported functions, types, variables, and constants. 
    Comments should explain *what* the code does and *why*, especially for non-obvious parts. 
    Use Godoc conventions for easily generated documentation.
    * **References:**
        * [Go Doc Comments - The Go Programming Language](https://go.dev/doc/comment)
        * [Documentation and Comments in Go - With Code Example](https://web.archive.org/web/20251015043026/https://withcodeexample.com/golang-documentation-and-comments-guide/)

10. **Thou Shalt Be Mindful of Performance and Allocations:**

    * **Guideline:** While Go is performant, be aware of potential bottlenecks. 
    Understand how slices, maps, and pointers work to minimize unnecessary allocations and garbage collection pressure, especially in performance-critical code. 
    Use tools like the pprof profiler to identify performance issues.

**Additional Recommended Reading and Style Guides:**

* [Effective Go](https://go.dev/doc/effective_go) - A fundamental guide from the Go team on writing clear, idiomatic Go code.
* [Go Code Review Comments](https://go.dev/wiki/CodeReviewComments) - A wiki page listing common style issues and suggestions.
* [Google Go Style Guide](https://google.github.io/styleguide/go/) - Google's internal style guide for Go.
* [Uber Go Style Guide](https://github.com/uber-go/guide) - Uber's widely referenced style guide for Go.

These resources provide more detailed explanations and examples for each of the guidelines mentioned above, helping you to write more effective and idiomatic Go code.