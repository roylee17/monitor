# Common Scripts

This directory contains shared utilities and functions used across all scripts.

## Files

### logging.sh

Common logging functions that provide consistent output formatting across all scripts.

#### Available Functions

**Basic Logging:**

- `log_info` - Display informational messages
- `log_error` - Display error messages (stderr)
- `log_warn` - Display warning messages
- `log_debug` - Display debug messages (only when DEBUG=true)
- `log_success` - Display success messages with ✅
- `log_failure` - Display failure messages with ❌
- `log_step` - Display step/phase messages
- `log_progress` - Display progress messages with ⚙️

**Formatting:**

- `print_header` - Display section headers with borders
- `print_subheader` - Display subsection headers
- `print_item` - Display bulleted list items
- `print_kv` - Display key-value pairs aligned

**Utilities:**

- `check_command` - Check if a command exists
- `check_namespace` - Check if a Kubernetes namespace exists
- `spinner` - Display a spinner for long operations
- `run_with_spinner` - Run a command with spinner

#### Color Variables

All color variables are exported for use in scripts:

- `COLOR_RED`, `COLOR_GREEN`, `COLOR_YELLOW`, `COLOR_BLUE`
- `COLOR_MAGENTA`, `COLOR_CYAN`, `COLOR_GRAY`, `COLOR_RESET`

#### Symbols

Unicode symbols for better visual feedback:

- `SYMBOL_CHECK` - ✅
- `SYMBOL_CROSS` - ❌
- `SYMBOL_WARN` - ⚠️
- `SYMBOL_INFO` - ℹ️
- `SYMBOL_ROCKET` - 🚀
- `SYMBOL_GEAR` - ⚙️
- `SYMBOL_CLOCK` - 🕐
- `SYMBOL_SEARCH` - 🔍

## Usage

Source the logging functions in your script:

```bash
#!/bin/bash
set -e

# Source common functions
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/common/logging.sh"

# Use logging functions
log_info "Starting process..."
log_success "Operation completed!"
```

### Overriding Functions

Some scripts override `log_info` to add timestamps:

```bash
# Override log_info to include timestamp
log_info() {
    echo -e "${COLOR_GREEN}[$(date +'%Y-%m-%d %H:%M:%S')]${COLOR_RESET} $*"
}
```

### Examples

See `logging-example.sh` for a complete demonstration of all available functions.

## Style Guidelines

1. Use `log_info` for general information
2. Use `log_error` for errors (automatically goes to stderr)
3. Use `log_warn` for warnings
4. Use `log_step` for major process steps
5. Use `log_success`/`log_failure` for final results
6. Use `print_header` for major sections
7. Use `print_subheader` for subsections
8. Use `print_item` for lists
9. Use `print_kv` for structured data display
