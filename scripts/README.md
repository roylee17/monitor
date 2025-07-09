# Scripts Directory

This directory contains utility scripts for managing and monitoring the ICS deployment.

## Subdirectories

- **`utils/`** - Shared utilities and logging functions
- **`clusters/`** - Kind cluster management and MetalLB utilities
- **`devnet/`** - Devnet config genereation
- **`lifecycle/`** - Consumer chain lifecycle management scripts

## Conventions

All scripts in this directory follow the conventions defined in [CONVENTIONS.md](CONVENTIONS.md).

Key points:

- All scripts use `#!/bin/bash` and `set -e`
- Consistent logging with functions from `common/logging.sh`
- Proper argument parsing with help text
- Meaningful exit codes (0=success, 1=error)
- Parameterized namespaces with sensible defaults
-
