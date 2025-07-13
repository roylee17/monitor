#!/usr/bin/env bash
# Logging utilities for scripts

# Source common if not already sourced
if [[ -z "$REPO_ROOT" ]]; then
    SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
    source "$SCRIPT_DIR/common.sh"
fi

# Define colors for terminal output
if [[ -t 1 ]]; then
    # Terminal supports colors
    export RED='\033[0;31m'
    export GREEN='\033[0;32m'
    export YELLOW='\033[0;33m'
    export BLUE='\033[0;34m'
    export NC='\033[0m'  # No Color
else
    # No color support
    export RED=''
    export GREEN=''
    export YELLOW=''
    export BLUE=''
    export NC=''
fi

# Log levels
export LOG_LEVEL_DEBUG=0
export LOG_LEVEL_INFO=1
export LOG_LEVEL_WARN=2
export LOG_LEVEL_ERROR=3

# Current log level (default: INFO)
export CURRENT_LOG_LEVEL="${LOG_LEVEL:-$LOG_LEVEL_INFO}"

# Timestamp format
get_timestamp() {
    date '+%Y-%m-%d %H:%M:%S'
}

# Log with color and level
log_message() {
    local level="$1"
    local color="$2"
    local prefix="$3"
    local message="$4"
    
    # Check if we should log this level
    local numeric_level
    case "$level" in
        DEBUG) numeric_level=$LOG_LEVEL_DEBUG ;;
        INFO)  numeric_level=$LOG_LEVEL_INFO ;;
        WARN)  numeric_level=$LOG_LEVEL_WARN ;;
        ERROR) numeric_level=$LOG_LEVEL_ERROR ;;
        *) numeric_level=$LOG_LEVEL_INFO ;;
    esac
    
    if [[ $numeric_level -ge $CURRENT_LOG_LEVEL ]]; then
        if [[ -t 1 ]]; then
            # Terminal output with color
            printf "${color}[${prefix}]${NC} %s\n" "${message}"
        else
            # Non-terminal output without color
            printf "[%s] [%s] %s\n" "$(get_timestamp)" "${prefix}" "${message}"
        fi
    fi
}

# Convenience functions
log_debug() {
    log_message "DEBUG" "$BLUE" "DEBUG" "$1"
}

log_info() {
    log_message "INFO" "$GREEN" "INFO" "$1"
}

log_warn() {
    log_message "WARN" "$YELLOW" "WARN" "$1"
}

log_error() {
    log_message "ERROR" "$RED" "ERROR" "$1"
}

# Log a separator line
log_separator() {
    echo "========================================"
}

# Log a header
log_header() {
    local header="$1"
    echo
    log_separator
    echo "$header"
    log_separator
}

# Check result and log appropriately
check_result() {
    local result=$1
    local success_msg="$2"
    local failure_msg="$3"
    
    if [[ $result -eq 0 ]]; then
        log_info "$success_msg"
        return 0
    else
        log_error "$failure_msg"
        return $result
    fi
}

# Progress indicator
show_progress() {
    local message="$1"
    echo -n "$message"
}

# Complete progress
complete_progress() {
    echo " ✓"
}

# Fail progress
fail_progress() {
    echo " ✗"
}

# Spinner for long operations
spinner() {
    local pid=$1
    local delay=0.1
    local spinstr='|/-\'
    
    while ps -p $pid > /dev/null 2>&1; do
        local temp=${spinstr#?}
        printf " [%c]  " "$spinstr"
        local spinstr=$temp${spinstr%"$temp"}
        sleep $delay
        printf "\b\b\b\b\b\b"
    done
    printf "    \b\b\b\b"
}

# Run command with spinner
run_with_spinner() {
    local message="$1"
    shift
    
    show_progress "$message"
    
    # Run command in background
    "$@" &
    local pid=$!
    
    # Show spinner
    spinner $pid
    
    # Wait for command to complete
    wait $pid
    local result=$?
    
    if [[ $result -eq 0 ]]; then
        complete_progress
    else
        fail_progress
    fi
    
    return $result
}

# Success/failure messages
log_success() {
    log_message "INFO" "$GREEN" "SUCCESS" "$1"
}

log_failure() {
    log_message "ERROR" "$RED" "FAILURE" "$1"
}

# Print functions for formatting
print_header() {
    local header="$1"
    echo
    echo "========================================"
    echo "$header"
    echo "========================================"
}

print_subheader() {
    local subheader="$1"
    echo
    echo "--- $subheader ---"
}

print_item() {
    local item="$1"
    echo "  • $item"
}

print_kv() {
    local key="$1"
    local value="$2"
    printf "  %-20s : %s\n" "$key" "$value"
}