# OpenClaw Bedrock Provider Setup

Files to configure OpenClaw to use Amazon Bedrock (GLM-5) as the default model.

## Quick Start

# 0. Update OpenClaw (recommended)

```bash
openclaw update
```

### 1. Create IAM User and Policy

```bash
./setup.sh
```

This creates:
- IAM policy `OpenClawBedrockAccess` with Bedrock invoke permissions
- IAM user `openclaw-bedrock`
- Attaches policy to user

### 2. Generate Access Keys

```bash
./create-keys.sh
```

Save the `AccessKeyId` and `SecretAccessKey` output.

### 3. Configure AWS Credentials

**Option A: Environment variables** (add to `~/.bashrc`):

```bash
export AWS_ACCESS_KEY_ID=your_access_key_id
export AWS_SECRET_ACCESS_KEY=your_secret_access_key
export AWS_REGION=us-east-1
```

**Option B: AWS credentials file**:

```bash
mkdir -p ~/.aws
cat > ~/.aws/credentials << 'EOF'
[default]
aws_access_key_id = your_access_key_id
aws_secret_access_key = your_secret_access_key
EOF

cat > ~/.aws/config << 'EOF'
[default]
region = us-east-1
EOF
```

**Option C: Set systemd unit config**:

If running as a systemd service (unlikely for integration testing), edit the unit:

```bash
systemctl --user edit openclaw-gateway
```

Then add:

```
[Service]
Environment="AWS_ACCESS_KEY_ID=your_access_key_id"
Environment="AWS_SECRET_ACCESS_KEY=your_secret_access_key"
Environment="AWS_REGION=us-east-1"
```

### 4. Update OpenClaw Config

Backup current config

```bash
cp ~/.openclaw/openclaw.json ~/.openclaw/openclaw.json.backup
```

#### Patch config
To switch to Bedrock GLM-5 as default, you need:

1. Add Bedrock auth profile to  openclaw.json :

```json
"auth": {
    "profiles": {
        "anthropic:default": {
            "provider": "anthropic",
            "mode": "api_key"
        },
        "amazon-bedrock:default": {
            "provider": "amazon-bedrock",
            "mode": "api_key"
        }
    }
}
```

2. Change the default model in  agents.defaults.model.primary :

```json
"agents": {
    "defaults": {
        "model": {
            "primary": "amazon-bedrock/zai.glm-5"
        }
    }
}
```

#### Restart gateway

```bash
openclaw gateway restart
```

#### Verify

```bash
openclaw status
```

## Files

| File | Purpose |
|------|---------|
| `setup.sh` | Create IAM policy and user |
| `create-keys.sh` | Generate access keys |
| `cleanup.sh` | Tear down IAM resources |

## Cleanup

To remove all IAM resources created by this setup:

```bash
./cleanup.sh
```

**Warning**: This deletes the `openclaw-bedrock` user and all their access keys.

## Notes

- The policy grants minimal permissions: invoke Bedrock models and list available models
- GLM-5 model ID: `amazon-bedrock/zai.glm-5`
- Change `AWS_REGION` to match your preferred Bedrock region (us-east-1, eu-west-1, etc.)
