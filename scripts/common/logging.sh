#!/bin/bash

# Common logging functions for all scripts
# Source this file to use consistent logging across scripts

# Colors
export COLOR_RED='\033[0;31m'
export COLOR_GREEN='\033[0;32m'
export COLOR_YELLOW='\033[1;33m'
export COLOR_BLUE='\033[0;34m'
export COLOR_MAGENTA='\033[0;35m'
export COLOR_CYAN='\033[0;36m'
export COLOR_GRAY='\033[0;90m'
export COLOR_RESET='\033[0m'

# Unicode symbols
export SYMBOL_CHECK='✅'
export SYMBOL_CROSS='❌'
export SYMBOL_WARN='⚠️'
export SYMBOL_INFO='ℹ️'
export SYMBOL_ROCKET='🚀'
export SYMBOL_GEAR='⚙️'
export SYMBOL_CLOCK='🕐'
export SYMBOL_SEARCH='🔍'

# Raw output mode - suppress all logging when true
# Usage: RAW_OUTPUT=true ./script.sh
export RAW_OUTPUT="${RAW_OUTPUT:-false}"

# Logging functions
log_info() {
    [ "${RAW_OUTPUT}" = "true" ] && return
    echo -e "${COLOR_GREEN}[INFO]${COLOR_RESET} $*"
}

log_error() {
    [ "${RAW_OUTPUT}" = "true" ] && return
    echo -e "${COLOR_RED}[ERROR]${COLOR_RESET} $*" >&2
}

log_warn() {
    [ "${RAW_OUTPUT}" = "true" ] && return
    echo -e "${COLOR_YELLOW}[WARN]${COLOR_RESET} $*"
}

log_debug() {
    [ "${RAW_OUTPUT}" = "true" ] && return
    if [ "${DEBUG:-false}" = "true" ]; then
        echo -e "${COLOR_GRAY}[DEBUG]${COLOR_RESET} $*"
    fi
}

log_success() {
    [ "${RAW_OUTPUT}" = "true" ] && return
    echo -e "${COLOR_GREEN}${SYMBOL_CHECK} $*${COLOR_RESET}"
}

log_failure() {
    [ "${RAW_OUTPUT}" = "true" ] && return
    echo -e "${COLOR_RED}${SYMBOL_CROSS} $*${COLOR_RESET}"
}

log_step() {
    [ "${RAW_OUTPUT}" = "true" ] && return
    echo -e "${COLOR_BLUE}[STEP]${COLOR_RESET} $*"
}

log_progress() {
    [ "${RAW_OUTPUT}" = "true" ] && return
    echo -e "${COLOR_CYAN}${SYMBOL_GEAR} $*${COLOR_RESET}"
}

# Print section headers
print_header() {
    [ "${RAW_OUTPUT}" = "true" ] && return
    local title="$1"
    local width=${2:-60}
    local border=$(printf '%*s' "$width" | tr ' ' '=')
    
    echo ""
    echo -e "${COLOR_BLUE}${border}${COLOR_RESET}"
    echo -e "${COLOR_BLUE}${title}${COLOR_RESET}"
    echo -e "${COLOR_BLUE}${border}${COLOR_RESET}"
}

print_subheader() {
    [ "${RAW_OUTPUT}" = "true" ] && return
    local title="$1"
    local width=${2:-40}
    local border=$(printf '%*s' "$width" | tr ' ' '-')
    
    echo ""
    echo -e "${COLOR_CYAN}${title}${COLOR_RESET}"
    echo -e "${COLOR_CYAN}${border}${COLOR_RESET}"
}

# Utility functions
check_command() {
    local cmd="$1"
    if ! command -v "$cmd" &> /dev/null; then
        log_error "Required command '$cmd' not found"
        return 1
    fi
    return 0
}

check_namespace() {
    local namespace="$1"
    if ! kubectl get namespace "$namespace" &> /dev/null; then
        log_error "Namespace '$namespace' does not exist"
        return 1
    fi
    return 0
}

# Spinner for long-running operations
spinner() {
    local pid=$1
    local delay=0.1
    local spinstr='|/-\'
    
    while [ "$(ps a | awk '{print $1}' | grep "$pid")" ]; do
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
    local msg="$1"
    shift
    
    echo -n "$msg"
    "$@" &
    local pid=$!
    spinner "$pid"
    wait "$pid"
    local result=$?
    
    if [ $result -eq 0 ]; then
        echo -e " ${COLOR_GREEN}✓${COLOR_RESET}"
    else
        echo -e " ${COLOR_RED}✗${COLOR_RESET}"
    fi
    
    return "$result"
}

# Print a list item
print_item() {
    [ "${RAW_OUTPUT}" = "true" ] && return
    echo "  • $*"
}

# Print key-value pair
print_kv() {
    [ "${RAW_OUTPUT}" = "true" ] && return
    local key="$1"
    local value="$2"
    printf "  %-20s : %s\n" "$key" "$value"
}

# Export all functions
export -f log_info log_error log_warn log_debug log_success log_failure
export -f log_step log_progress print_header print_subheader
export -f check_command check_namespace spinner run_with_spinner
export -f print_item print_kv