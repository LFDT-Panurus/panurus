#!/bin/bash

REPO="LFDT-Panurus/panurus"
API_URL="https://api.github.com/repos/$REPO"
SEARCH_API_URL="https://api.github.com/search/issues"

# Require jq and gh for JSON parsing and GitHub API
if ! command -v jq &> /dev/null; then
    echo "Error: jq is required but not installed. Please install it (e.g., brew install jq or apt install jq)." >&2
    exit 1
fi

if ! command -v gh &> /dev/null; then
    echo "Error: gh (GitHub CLI) is required but not installed. Please install it (e.g., brew install gh)." >&2
    exit 1
fi

# Function to format numbers with commas (Cross-platform pure bash)
format_num() {
    local num=$1
    while [[ $num =~ ^([0-9]+)([0-9]{3})(.*)$ ]]; do
        num="${BASH_REMATCH[1]},${BASH_REMATCH[2]}${BASH_REMATCH[3]}"
    done
    echo "$num"
}

# Helper to call GitHub API using gh (handles authentication automatically)
call_api() {
    local endpoint="$1"
    gh api "$endpoint" --jq '.'
}

# Helper to fetch total counts using the paginated Link header
get_paginated_count() {
    local endpoint="$1"

    # Fetch headers only using gh
    local header_file=$(mktemp)
    gh api "$endpoint" -i > "$header_file" 2>/dev/null

    local link_header=$(grep -i '^link:' "$header_file")
    if [[ -n "$link_header" ]]; then
        # Extract the exact page number from the 'last' rel link
        grep -i '^link:' "$header_file" | sed -E 's/.*<([^>]+)>; rel="last".*/\1/' | sed -E 's/.*page=([0-9]+).*/\1/'
    else
        # Fallback to counting the JSON array length if there is only 1 page
        local items=$(call_api "$endpoint" | jq -r 'length // 0')
        echo "$items"
    fi
    rm -f "$header_file"
}

# Helper to get issues/PRs counts from Search API
get_search_count() {
    local query="$1"
    gh api "$SEARCH_API_URL?q=repo:$REPO+$query" --jq '.total_count // 0'
}

echo "Fetching repository metrics..." >&2
REPO_DATA=$(call_api "$API_URL")

# 1. Repository Metrics
REPO_URL=$(echo "$REPO_DATA" | jq -r '.html_url // ""')
LANGUAGE=$(echo "$REPO_DATA" | jq -r '.language // "Unknown"')
LICENSE=$(echo "$REPO_DATA" | jq -r '.license.name // "None"')
STARS=$(echo "$REPO_DATA" | jq -r '.stargazers_count // 0')
FORKS=$(echo "$REPO_DATA" | jq -r '.forks_count // 0')
WATCHERS=$(echo "$REPO_DATA" | jq -r '.subscribers_count // 0')
DEFAULT_BRANCH=$(echo "$REPO_DATA" | jq -r '.default_branch // "main"')

echo "Fetching contributors count..." >&2
CONTRIBUTORS=$(get_paginated_count "$API_URL/contributors?per_page=1&anon=true")

# 2. Community Standards Compliance
echo "Checking community standards..." >&2
check_file() {
    local file=$1
    local status=$(curl -s -o /dev/null -w "%{http_code}" "https://raw.githubusercontent.com/$REPO/$DEFAULT_BRANCH/$file")
    if [[ "$status" == "200" ]]; then
        # Output status with a Markdown link to the file on GitHub
        echo "✅ [Complete](https://github.com/$REPO/blob/$DEFAULT_BRANCH/$file)"
    else
        echo "❌ Missing"
    fi
}

README_STATUS=$(check_file "README.md")
CONTRIBUTING_STATUS=$(check_file "CONTRIBUTING.md")
CODE_OF_CONDUCT_STATUS=$(check_file "CODE_OF_CONDUCT.md")
LICENSE_STATUS=$(check_file "LICENSE")
SECURITY_STATUS=$(check_file "SECURITY.md")

# 3. Development Activity
echo "Fetching development activity metrics..." >&2

# Cross-platform date parsing (macOS/BSD vs GNU Linux)
if date --version >/dev/null 2>&1; then
    SINCE_DATE=$(date -d "30 days ago" -u +"%Y-%m-%dT%H:%M:%SZ")
else
    SINCE_DATE=$(date -v-30d -u +"%Y-%m-%dT%H:%M:%SZ")
fi

COMMITS_30D=$(get_paginated_count "$API_URL/commits?since=$SINCE_DATE&per_page=1")
OPEN_ISSUES=$(get_search_count "type:issue+state:open")
CLOSED_ISSUES=$(get_search_count "type:issue+state:closed")
OPEN_PRS=$(get_search_count "type:pr+state:open")
CLOSED_PRS=$(get_search_count "type:pr+state:closed")

# Get current date for the header
CURRENT_DATE=$(date +"%B %d, %Y at %H:%M %Z")

# Output Final Markdown
echo ""
cat <<EOF
## Project Statistics

The following metrics demonstrate the project's maturity, active development, and community engagement ($CURRENT_DATE):

### Repository Metrics

| Metric | Value |
|--------|-------|
| **Repository** | [github.com/$REPO]($REPO_URL) |
| **Primary Language** | $LANGUAGE |
| **License** | $LICENSE |
| **Stars** | $(format_num "$STARS") |
| **Forks** | $(format_num "$FORKS") |
| **Watchers** | $(format_num "$WATCHERS") |
| **Total Contributors** | $(format_num "$CONTRIBUTORS") |

### Community Standards Compliance
| Standard | Status |
|----------|--------|
| README | $README_STATUS |
| CONTRIBUTING | $CONTRIBUTING_STATUS |
| CODE_OF_CONDUCT | $CODE_OF_CONDUCT_STATUS |
| LICENSE | $LICENSE_STATUS |
| SECURITY Policy | $SECURITY_STATUS |

### Development Activity
| Metric | Count |
|--------|-------|
| **Commits (Last 30 Days)** | $(format_num "$COMMITS_30D") |
| **Open Issues** | $(format_num "$OPEN_ISSUES") |
| **Closed Issues** | $(format_num "$CLOSED_ISSUES") |
| **Open Pull Requests** | $(format_num "$OPEN_PRS") |
| **Closed Pull Requests** | $(format_num "$CLOSED_PRS") |
EOF