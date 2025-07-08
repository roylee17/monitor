#!/bin/bash
set -e

# Source common functions
THIS_SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$THIS_SCRIPT_DIR/../utils/common.sh"
source "$THIS_SCRIPT_DIR/../utils/logging.sh"

# Function to install MetalLB in a cluster using Helm
install_metallb() {
    local cluster=$1
    local ip_range_start=$2
    local ip_range_end=$3
    
    log_info "Installing MetalLB in $cluster cluster using Helm..."
    
    # Switch context
    kubectl config use-context "kind-${cluster}-cluster"
    
    # Add MetalLB Helm repository if not already added
    if ! helm repo list | grep -q metallb; then
        helm repo add metallb https://metallb.github.io/metallb
        helm repo update
    fi
    
    # Install MetalLB using Helm
    helm upgrade --install metallb metallb/metallb \
        --namespace metallb-system \
        --create-namespace \
        --wait \
        --timeout 2m \
        --set speaker.tolerations[0].effect=NoSchedule \
        --set speaker.tolerations[0].operator=Exists \
        --set controller.tolerations[0].effect=NoSchedule \
        --set controller.tolerations[0].operator=Exists
    
    # Wait a moment for CRDs to be fully established
    sleep 5
    
    # Create IPAddressPool and L2Advertisement
    cat <<EOF | kubectl apply -f -
apiVersion: metallb.io/v1beta1
kind: IPAddressPool
metadata:
  name: kind-pool
  namespace: metallb-system
spec:
  addresses:
  - ${ip_range_start}-${ip_range_end}
---
apiVersion: metallb.io/v1beta1
kind: L2Advertisement
metadata:
  name: kind-advertisement
  namespace: metallb-system
spec:
  ipAddressPools:
  - kind-pool
EOF
    
    log_info "MetalLB installed successfully in $cluster with IP range ${ip_range_start}-${ip_range_end}"
}

# Function to create MetalLB values file for more control
create_metallb_values() {
    local cluster=$1
    local values_file="/tmp/metallb-values-${cluster}.yaml"
    
    cat > "$values_file" <<EOF
# MetalLB configuration for Kind cluster
controller:
  tolerations:
  - effect: NoSchedule
    operator: Exists
  
speaker:
  tolerations:
  - effect: NoSchedule
    operator: Exists
  # Kind clusters need special handling for L2 mode
  frr:
    enabled: false

# Prometheus metrics (optional)
prometheus:
  serviceMonitor:
    enabled: false
  prometheusRule:
    enabled: false
EOF
    
    echo "$values_file"
}

# Main execution
main() {
    log_info "Installing MetalLB in all Kind clusters..."
    
    # Check if helm is installed
    if ! command -v helm &> /dev/null; then
        log_error "Helm is not installed. Please install Helm first."
        log_info "Visit: https://helm.sh/docs/intro/install/"
        exit 1
    fi
    
    # Get Kind network subnet (usually 172.18.0.0/16 or 172.19.0.0/16)
    local kind_subnet=$(docker network inspect kind -f '{{(index .IPAM.Config 0).Subnet}}' 2>/dev/null || echo "172.18.0.0/16")
    local subnet_prefix=$(echo $kind_subnet | cut -d'.' -f1-3)  # Get first 3 octets (e.g., 192.168.97)
    
    log_info "Using subnet prefix: $subnet_prefix for LoadBalancer IPs"
    
    # Install MetalLB in each cluster with unique IP ranges within the actual subnet
    install_metallb "alice" "${subnet_prefix}.100" "${subnet_prefix}.109"
    install_metallb "bob" "${subnet_prefix}.110" "${subnet_prefix}.119"
    install_metallb "charlie" "${subnet_prefix}.120" "${subnet_prefix}.129"
    
    log_info ""
    log_info "MetalLB installation complete in all clusters!"
    log_info ""
    log_info "LoadBalancer IP ranges:"
    log_info "  Alice:   ${subnet_prefix}.100 - ${subnet_prefix}.109"
    log_info "  Bob:     ${subnet_prefix}.110 - ${subnet_prefix}.119"
    log_info "  Charlie: ${subnet_prefix}.120 - ${subnet_prefix}.129"
    log_info ""
    log_info "You can verify the installation with:"
    log_info "  kubectl --context kind-alice-cluster get pods -n metallb-system"
}

# Run main function
main "$@"