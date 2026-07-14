# Documentation Guide

This guide outlines how to contribute to, test, build, and deploy the static documentation site for Panurus.

Documentation is built using [MkDocs](https://www.mkdocs.org/) with the premium, responsive [Material for MkDocs theme](https://squidfunk.github.io/mkdocs-material/).

---

## 🛠️ Local Development Setup

To preview changes locally, you will need Python 3 installed on your system.

### 1. Install Dependencies
Install the pinned documentation tools (including MkDocs and the Material theme) using the requirements file:

```bash
make docs-install
```

This target runs:
```bash
pip install -r requirements.txt
```

### 2. Live Preview During Editing
You can start a local development server that watches for file changes and automatically refreshes your browser:

```bash
make docs-serve
```

Once started, open your browser and navigate to:
👉 **`http://127.0.0.1:8000`**

### 3. Build Static Site Locally
To verify that all links are resolved and the site builds cleanly without any warnings:

```bash
make docs-build
```

This runs the strict compiler command:
```bash
mkdocs build --strict
```

> [!IMPORTANT]
> The `--strict` flag ensures that the build will fail if there are any warnings (such as orphaned files or broken internal links). Always verify your build with `make docs-build` before committing changes!

---

## 🚀 GitHub Pages Deployment Setup

The static documentation site is automatically built and deployed to GitHub Pages whenever changes are merged into the `main` branch. 

If this is being set up on a new fork or repository for the first time, follow these steps to configure GitHub Pages:

1. **Navigate to Repository Settings**:
   Go to your repository page on GitHub, click on the **Settings** tab.
2. **Access Pages Settings**:
   In the left sidebar under the "Code and automation" section, click on **Pages**.
3. **Configure Build and Deployment**:
   - Under **Build and deployment** -> **Source**, select **"Deploy from a branch"**.
   - Under **Branch**, select `gh-pages` and set the folder to `/ (root)`.
   - Click **Save**.

The GitHub Actions workflow `.github/workflows/docs.yml` will automatically create and update the `gh-pages` branch on every merge.

---

## 📂 Documentation Directory Structure

All documentation source markdown files are located in the `docs/` directory of the project:

```
docs/
├── README.md               # Home page
├── configuration.md         # Configuration guide
├── development/            # Developer guidelines and guides
│   ├── ai_agents.md        # AI Agent context
│   ├── documentation.md    # This guide
│   └── ...                 # Other development guidelines
├── drivers/                # Fabric and ZK driver specs
├── services/               # Core services (auditor, storage, ttx, etc.)
└── imgs/                   # Images and static assets
```

### Adding New Documentation Pages
1. Write your markdown file and place it in the appropriate subdirectory of `docs/`.
2. Add your page to the navigation hierarchy in `mkdocs.yml` located at the root of the project.
3. Validate your changes locally using `make docs-serve` or `make docs-build`.
