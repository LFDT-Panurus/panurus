#!/usr/bin/env bash
# fetch_contributors.sh — Run from inside your panurus folder

set -euo pipefail

REPO=$(gh repo view --json nameWithOwner -q .nameWithOwner)

echo "📦 Contributors for: $REPO"
echo ""
echo "| Name (@username) | Company | Location | Email | Twitter | Followers | Public Repos | Commits |"
echo "|------------------|---------|----------|-------|---------|-----------|--------------|---------|"

# Step 1: Get contributor stats (additions, deletions, commits per week)
STATS=$(gh api --cache 1h "/repos/$REPO/stats/contributors")

# Step 2: Get paginated contributor list and enrich each with user profile
# Using process substitution to avoid subshell issues with variable scope
CONTRIBUTOR_LOGINS=()
COUNT=0

while read -r LOGIN; do
  CONTRIBUTOR_LOGINS+=("$LOGIN")
  ((COUNT++))

  # Fetch full user profile
  USER=$(gh api "/users/$LOGIN")

  # Fetch per-contributor stats from cached stats payload
  # Handle case where stats might be empty or not available
  if [ "$STATS" != "[]" ] && [ "$STATS" != "{}" ]; then
    CONTRIB_STATS=$(echo "$STATS" | jq --arg login "$LOGIN" '
      .[] | select(.author.login == $login) | {
        commits: ([.weeks[].c] | add // 0),
        additions: ([.weeks[].a] | add // 0),
        deletions: ([.weeks[].d] | add // 0)
      }')

    COMMITS=$(echo "$CONTRIB_STATS"   | jq -r '.commits   // 0')
    ADDITIONS=$(echo "$CONTRIB_STATS" | jq -r '.additions // 0')
    DELETIONS=$(echo "$CONTRIB_STATS" | jq -r '.deletions // 0')
  else
    # Fallback: get contributor data from the contributors list and extract contributions count
    # We need to get the contributors list again to find this specific user
    CONTRIB_DATA=$(gh api --paginate \
      -H "Accept: application/vnd.github+json" \
      "/repos/$REPO/contributors" \
      --jq ".[] | select(.login == \"$LOGIN\")")

    COMMITS=$(echo "$CONTRIB_DATA" | jq -r '.contributions // 0')
    ADDITIONS="N/A"  # These aren't available without stats
    DELETIONS="N/A"  # These aren't available without stats
  fi

  # Extract user information
  NAME=$(echo "$USER" | jq -r '.name // .login')
  COMPANY=$(echo "$USER" | jq -r '.company // "N/A"')
  LOCATION=$(echo "$USER" | jq -r '.location // "N/A"')
  EMAIL=$(echo "$USER" | jq -r '.email // "N/A"')
  TWITTER=$(echo "$USER" | jq -r '.twitter_username // "N/A"')
  if [ "$TWITTER" != "N/A" ]; then
    TWITTER="@$TWITTER"
  fi
  FOLLOWERS=$(echo "$USER" | jq -r '.followers')
  PUBLIC_REPOS=$(echo "$USER" | jq -r '.public_repos')

  # Print main row
  echo "| $NAME (@$LOGIN) | $COMPANY | $LOCATION | $EMAIL | $TWITTER | $FOLLOWERS | $PUBLIC_REPOS | $COMMITS |"

  # Print social links if available (indented below the row)
  BLOG=$(echo "$USER" | jq -r '.blog // empty')
  LINKEDIN_URL=""

  # Check for LinkedIn in blog field
  if [ -n "$BLOG" ] && [ "$BLOG" != "N/A" ]; then
    if [[ "$BLOG" == *"linkedin.com"* ]]; then
      LINKEDIN_URL="$BLOG"
    fi
  fi

  # Fetch social accounts to find LinkedIn if not found in blog
  if [ -z "$LINKEDIN_URL" ]; then
    SOCIAL_ACCOUNTS=$(gh api "/users/$LOGIN/social_accounts")
    LINKEDIN_URL=$(echo "$SOCIAL_ACCOUNTS" | jq -r '.[] | select(.provider == "linkedin") | .url // empty')
  fi

  if [ -n "$LINKEDIN_URL" ]; then
    echo "| *💼 LinkedIn:* $LINKEDIN_URL | | | | | | | |"
  fi

  if [ "$TWITTER" != "N/A" ]; then
    echo "| *🐦 Twitter:* https://twitter.com/${TWITTER#@} | | | | | | | |"
  fi

  if [ -n "$BLOG" ] && [ "$BLOG" != "N/A" ] && [ -z "$LINKEDIN_URL" ]; then
    echo "| *🌐 Website:* $BLOG | | | | | | | |"
  fi

done < <(gh api --paginate \
  -H "Accept: application/vnd.github+json" \
  "/repos/$REPO/contributors" \
  --jq '.[] | select(.login != "dependabot[bot]") | .login')

# Summary section
echo ""
echo "📊 SUMMARY"
echo ""
echo "**Total Contributors:** $COUNT"

# Identify IBM contributors (using already fetched data to avoid extra API calls)
IBM_CONTRIBUTORS=()
for LOGIN in "${CONTRIBUTOR_LOGINS[@]}"; do
  # We need to fetch user data to check company/email
  USER_DATA=$(gh api "/users/$LOGIN")
  COMPANY=$(echo "$USER_DATA" | jq -r '.company // ""')
  EMAIL=$(echo "$USER_DATA" | jq -r '.email // ""')
  NAME=$(echo "$USER_DATA" | jq -r '.name // .login')

  # Check if company, email, or name indicates IBM affiliation
  if [[ "$COMPANY" == *"IBM"* ]] || [[ "$COMPANY" == *"ibm"* ]] || [[ "$EMAIL" == *"ibm.com"* ]] || [[ "$EMAIL" == *"zurich.ibm.com"* ]] || [[ "$NAME" == *"IBM"* ]] || [[ "$NAME" == *"ibm"* ]]; then
    IBM_CONTRIBUTORS+=("$NAME (@$LOGIN)")
  fi
done

IBM_COUNT=${#IBM_CONTRIBUTORS[@]}
echo "**IBM Contributors:** $IBM_COUNT"

if [ $IBM_COUNT -gt 0 ]; then
  echo ""
  echo "**IBM Contributor List:**"
  for contributor in "${IBM_CONTRIBUTORS[@]}"; do
    echo "- $contributor"
  done
fi

echo ""
echo "✅ Done"