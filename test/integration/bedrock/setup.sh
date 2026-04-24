#!/bin/bash
# OpenClaw Bedrock IAM Setup
# Creates IAM policy, user, and generates access keys for Bedrock access

set -e

USER_NAME="openclaw-bedrock"
POLICY_NAME="OpenClawBedrockAccess"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

echo "Creating IAM policy for Bedrock access..."

# Create policy document
cat > /tmp/bedrock-policy.json << 'EOF'
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "bedrock:InvokeModel",
        "bedrock:InvokeModelWithResponseStream"
      ],
      "Resource": "*"
    },
    {
      "Effect": "Allow",
      "Action": [
        "bedrock:ListFoundationModels",
        "bedrock:GetFoundationModel"
      ],
      "Resource": "*"
    }
  ]
}
EOF

# Get account ID
ACCOUNT_ID=$(aws sts get-caller-identity --query Account --output text)
POLICY_ARN="arn:aws:iam::${ACCOUNT_ID}:policy/${POLICY_NAME}"

# Create the policy (or use existing if already created)
aws iam create-policy \
  --policy-name "$POLICY_NAME" \
  --policy-document file:///tmp/bedrock-policy.json \
  2>/dev/null || echo "Policy already exists: $POLICY_ARN"

echo "Policy ARN: $POLICY_ARN"

echo "Creating IAM user: $USER_NAME..."

aws iam create-user --user-name "$USER_NAME" 2>/dev/null || echo "User already exists"

echo "Attaching policy to user..."

aws iam attach-user-policy \
  --user-name "$USER_NAME" \
  --policy-arn "$POLICY_ARN"

echo ""
echo "✓ User created and policy attached!"
echo ""
echo "Now run: ./create-keys.sh"
echo ""

# Cleanup
rm -f /tmp/bedrock-policy.json
