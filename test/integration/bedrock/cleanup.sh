#!/bin/bash
# Tear down OpenClaw Bedrock IAM resources
# WARNING: This deletes the user and all their access keys!

set -e

USER_NAME="openclaw-bedrock"
POLICY_NAME="OpenClawBedrockAccess"
ACCOUNT_ID=$(aws sts get-caller-identity --query Account --output text)
POLICY_ARN="arn:aws:iam::${ACCOUNT_ID}:policy/${POLICY_NAME}"

echo "This will delete:"
echo "  - IAM user: $USER_NAME"
echo "  - IAM policy: $POLICY_NAME"
echo "  - All access keys for this user"
echo ""
read -p "Are you sure? (yes/no): " confirm

if [ "$confirm" != "yes" ]; then
  echo "Aborted."
  exit 1
fi

echo ""
echo "Deleting access keys..."

# List and delete all access keys
for key_id in $(aws iam list-access-keys --user-name "$USER_NAME" --query 'AccessKeyMetadata[].AccessKeyId' --output text 2>/dev/null); do
  echo "  Deleting key: $key_id"
  aws iam delete-access-key --user-name "$USER_NAME" --access-key-id "$key_id"
done

echo "Detaching policy from user..."

aws iam detach-user-policy \
  --user-name "$USER_NAME" \
  --policy-arn "$POLICY_ARN" 2>/dev/null || true

echo "Deleting user..."

aws iam delete-user --user-name "$USER_NAME" 2>/dev/null || echo "User already deleted"

echo "Deleting policy..."

aws iam delete-policy --policy-arn "$POLICY_ARN" 2>/dev/null || echo "Policy already deleted"

echo ""
echo "✓ Cleanup complete!"
