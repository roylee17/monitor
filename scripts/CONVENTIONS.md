# Shell Script Style Guide

This style guide defines the conventions used throughout the scripts in this project.

## File Structure

### 1. File Header
```bash
#!/bin/bash
# Brief description of what the script does

set -e
```

### 2. Source Dependencies
```bash
# Source common functions
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/../common/logging.sh"
source "${SCRIPT_DIR}/consumer-utils.sh"
```

### 3. Configuration Section
```bash
# Command line options
VARIABLE_NAME=""
FLAG_NAME=false
DEFAULT_VALUE=30
```

### 4. Section Organization
Use clear section headers:
```bash
# ============================================
# Section Name
# ============================================
```

Common sections in order:
1. Configuration
2. Functions
3. Main

## Naming Conventions

### Variables
- **UPPERCASE** for global constants and configuration: `DEFAULT_NAMESPACE`
- **lowercase** for local variables: `local pod_name`
- **Descriptive names**: Use `consumer_id` not `id`
- **Boolean flags**: Use `true`/`false` strings: `SHOW_DETAILS=false`

### Functions
- **lowercase with underscores**: `get_validator_pod()`
- **Action-oriented names**: `validate_consumer_id()`, `display_consumer_details()`
- **Clear purpose**: One function, one responsibility

## Function Style

### Basic Structure
```bash
function_name() {
    local param1="$1"
    local param2="${2:-default_value}"
    local namespace="${3:-$DEFAULT_NAMESPACE}"
    
    # Validation
    if [ -z "$param1" ]; then
        log_error "Missing required parameter"
        return 1
    fi
    
    # Function logic
    local result=$(some_command)
    
    # Return value or echo result
    echo "$result"
}
```

### Best Practices
1. Declare `local` variables at the top
2. Validate parameters early
3. Use meaningful return codes (0 for success, 1 for error)
4. Echo results for captured output, return codes for status

## Command Line Interface

### Usage Function
Every script must have a comprehensive usage function:
```bash
usage() {
    cat << EOF
Usage: $0 [OPTIONS] ARGUMENTS

Brief description of the script's purpose.

Arguments:
  ARGUMENT             Description of required argument

Options:
  -s, --short          Short description
  -l, --long VALUE     Option with value
  -h, --help           Show this help message

Examples:
  $0                   # Basic usage
  $0 -s argument       # Example with option
  $0 --long=value arg  # Long option format
EOF
}
```

### Argument Parsing
```bash
parse_arguments() {
    # Check for help first
    if [[ "$1" == "-h" ]] || [[ "$1" == "--help" ]] || [[ $# -eq 0 ]]; then
        usage
        exit 0
    fi
    
    # Parse positional arguments first if needed
    REQUIRED_ARG="$1"
    shift
    
    # Parse options
    while [[ $# -gt 0 ]]; do
        case $1 in
            -s|--short)
                FLAG=true
                shift
                ;;
            -l|--long)
                VALUE="$2"
                shift 2
                ;;
            *)
                log_error "Unknown option: $1"
                usage
                exit 1
                ;;
        esac
    done
}
```

## Error Handling & Logging

### Logging Functions
Use consistent logging functions from `logging.sh`:
- `log_info "Message"` - General information
- `log_error "Message"` - Errors (outputs to stderr)
- `log_warn "Message"` - Warnings
- `log_success "Message"` - Success messages
- `log_step "Step description"` - Major operation steps

### Error Handling
```bash
# Check command success
if ! command_that_might_fail; then
    log_error "Failed to perform operation"
    exit 1
fi

# Capture and check output
local result=$(command 2>&1)
if [ $? -ne 0 ]; then
    log_error "Command failed: $result"
    return 1
fi
```

## Code Patterns

### Main Function Pattern
```bash
# ============================================
# Main
# ============================================

main() {
    parse_arguments "$@"
    
    # Validation
    local pod=$(get_validator_pod)
    if [ -z "$pod" ]; then
        exit 1
    fi
    
    # Main logic
    perform_operation "$pod"
}

# Run main
main "$@"
```

### Parameter Defaults
```bash
# Environment variable with default
DEFAULT_NAMESPACE="${NAMESPACE:-provider}"

# Function parameter with default
local namespace="${2:-$DEFAULT_NAMESPACE}"

# Inline default
local value="${VARIABLE:-default}"
```

### Command Execution
```bash
# Quote all variables
kubectl exec -n "$namespace" "pod/$pod" -- command

# Use $() for command substitution
local result=$(command)

# Use [[ ]] for conditionals
if [[ "$variable" == "value" ]]; then
    # action
fi

# Check numeric values
if [[ "$number" =~ ^[0-9]+$ ]]; then
    # valid number
fi
```

## Display & Output

### Structured Output
Use display functions from `logging.sh`:
```bash
print_header "Main Section"
print_subheader "Subsection"
print_kv "Key" "Value"
print_item "List item"
```

### Table Format
```bash
printf "  %-4s %-30s %-25s\n" "ID" "Chain ID" "Phase"
printf "  %-4s %-30s %-25s\n" "----" "------------------------------" "-------------------------"
```

### Color Usage
- Use color variables from `logging.sh`: `${COLOR_GREEN}`, `${COLOR_RED}`, etc.
- Always reset: `${COLOR_RESET}`
- Use semantic colors (e.g., green for success, red for errors)

## Best Practices

### General
1. **Always quote variables**: `"$variable"` to handle spaces
2. **Check if variables are empty**: `if [ -z "$var" ]; then`
3. **Use meaningful exit codes**: 0 for success, 1 for general errors
4. **Avoid global variables** where possible
5. **Make scripts idempotent** when appropriate

### Kubernetes Operations
1. **Parameterize namespace**: Allow `NAMESPACE` environment variable
2. **Check pod existence** before operations
3. **Handle kubectl errors** gracefully
4. **Use labels** for pod selection

### File Operations
1. **Use absolute paths** when possible
2. **Check file existence** before reading
3. **Use temp files carefully**: `/tmp/filename.$$`
4. **Clean up temp files** in exit traps if needed

## Testing

### Test Scripts
1. **Clear test descriptions** in output
2. **Show progress** for long operations
3. **Explicit pass/fail** status
4. **Exit codes** reflect test results
5. **Cleanup** at start of test

### Validation Functions
```bash
validate_input() {
    local input="$1"
    
    if ! [[ "$input" =~ ^[valid-pattern]$ ]]; then
        log_error "Invalid input: $input"
        return 1
    fi
    return 0
}
```

## Documentation

### Inline Comments
- Use comments for complex logic
- Explain "why" not "what"
- Keep comments up to date

### Function Documentation
For complex functions, add a comment block:
```bash
# Calculate spawn time from delay in seconds
# Arguments:
#   $1 - Delay in seconds (optional, defaults to DEFAULT_SPAWN_DELAY)
# Returns:
#   Spawn time in ISO format
calculate_spawn_time() {
    # function implementation
}
```

## Environment Variables

Common environment variables that scripts should respect:
- `NAMESPACE` - Kubernetes namespace (default: provider)
- `VALIDATOR` - Validator name (default: alice)
- `CONSUMER_NAMESPACE` - Namespace for consumer chains

Always provide defaults and allow overrides.