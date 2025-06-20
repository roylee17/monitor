# Scripts Directory

This directory contains utility scripts for managing and monitoring the ICS deployment.

## Scripts

### `status.sh`

Display comprehensive status of the ICS deployment including provider chain, validators, and consumer chains.

```bash
# Show full status
./scripts/status.sh

# Show only provider status
./scripts/status.sh --provider

# Show only consumer chains status
./scripts/status.sh --consumers

# Show detailed information
./scripts/status.sh --verbose

# Output as JSON (for automation)
./scripts/status.sh --json

# Override namespace
NAMESPACE=custom ./scripts/status.sh
# or
./scripts/status.sh --namespace custom
```

Features:

- Color-coded health status (green=healthy, red=error)
- Provider pod status with phase and IP
- Validator information and voting power
- Blockchain sync status (height, time, hash)
- Consumer chain listing with phases
- Consumer namespace pod status
- JSON output for automation
- Verbose mode for additional details

### Subdirectories

- **`common/`** - Shared utilities and logging functions
- **`lifecycle/`** - Consumer chain lifecycle management scripts
- **`hooks/`** - Git hooks and development tools

## Conventions

All scripts in this directory follow the conventions defined in [CONVENTIONS.md](CONVENTIONS.md).

Key points:

- All scripts use `#!/bin/bash` and `set -e`
- Consistent logging with functions from `common/logging.sh`
- Proper argument parsing with help text
- Meaningful exit codes (0=success, 1=error)
- Parameterized namespaces with sensible defaults
-
