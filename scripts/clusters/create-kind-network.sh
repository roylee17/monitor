#!/bin/bash
set -e

# Source logging utilities
THIS_SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$THIS_SCRIPT_DIR/../utils/logging.sh"

# Check if kind network exists
check_kind_network() {
    docker network inspect kind >/dev/null 2>&1
}

# Create kind network (let Docker choose subnet)
create_kind_network() {
    log_info "Creating kind network..."
    docker network create --driver=bridge kind
}

# Main execution
main() {
    if check_kind_network; then
        log_info "Kind network already exists"
        
        # Show the subnet being used
        subnet=$(docker network inspect kind --format='{{(index .IPAM.Config 0).Subnet}}')
        log_info "Kind network exists with subnet: $subnet"
    else
        create_kind_network
        log_info "Kind network created successfully"
    fi
}

# Run if executed directly
if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
    main "$@"
fi