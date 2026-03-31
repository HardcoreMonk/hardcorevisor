#!/usr/bin/env bash
# GitHub Branch Protection 규칙 설정
# gh CLI로 main/develop 브랜치에 보호 규칙을 적용한다.
# 사용법: ./scripts/setup-branch-protection.sh
# 사전 요구: gh auth login
set -euo pipefail

REPO="HardcoreMonk/hardcorevisor"

echo "=== Setting up branch protection rules ==="

# main 브랜치 보호
echo "Configuring main branch..."
gh api repos/$REPO/branches/main/protection -X PUT --input - <<'EOF'
{
  "required_status_checks": {
    "strict": true,
    "contexts": [
      "Rust Lint",
      "Rust Test",
      "Rust Coverage (>= 80%)",
      "Go Lint",
      "Go Test + Coverage (>= 70%)",
      "E2E Integration Tests",
      "CGo Integration Build",
      "Helm Lint",
      "Security Audit",
      "Docker Build",
      "WebUI Build + Test"
    ]
  },
  "enforce_admins": false,
  "required_pull_request_reviews": {
    "required_approving_review_count": 1,
    "dismiss_stale_reviews": true,
    "require_code_owner_reviews": false
  },
  "restrictions": null,
  "required_linear_history": false,
  "allow_force_pushes": false,
  "allow_deletions": false
}
EOF
echo "  main: done"

# develop 브랜치 보호
echo "Configuring develop branch..."
gh api repos/$REPO/branches/develop/protection -X PUT --input - <<'EOF'
{
  "required_status_checks": {
    "strict": false,
    "contexts": [
      "Rust Test",
      "Go Test + Coverage (>= 70%)",
      "E2E Integration Tests",
      "WebUI Build + Test"
    ]
  },
  "enforce_admins": false,
  "required_pull_request_reviews": {
    "required_approving_review_count": 1,
    "dismiss_stale_reviews": false,
    "require_code_owner_reviews": false
  },
  "restrictions": null,
  "required_linear_history": false,
  "allow_force_pushes": false,
  "allow_deletions": false
}
EOF
echo "  develop: done"

echo ""
echo "=== Branch protection configured ==="
echo ""
echo "main: 11 required checks + 1 review + no force push"
echo "develop: 4 required checks + 1 review + no force push"
