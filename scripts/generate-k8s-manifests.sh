#!/bin/bash

# Generate Kubernetes Manifests from Testnet Assets
#
# This script takes the validator home directories created by testnet-coordinator.sh
# and generates Kubernetes manifests for deployment.

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

# Configuration
ASSETS_DIR="$PROJECT_ROOT/k8s/testnet/assets"
OUTPUT_DIR="$PROJECT_ROOT/k8s/testnet/generated"
NAMESPACE="provider"
VALIDATORS=("alice" "bob" "charlie")

# Source common functions
source "${SCRIPT_DIR}/common/logging.sh"

# Override log_info to include timestamp (using variables from logging.sh)
log_info() {
    echo -e "${COLOR_GREEN}[$(date +'%Y-%m-%d %H:%M:%S')]${COLOR_RESET} $*"
}

# error function that exits
error() {
    log_error "$@"
    exit 1
}

# Check if assets exist
check_assets() {
    if [ ! -d "$ASSETS_DIR" ]; then
        error "Assets directory not found. Run testnet-coordinator.sh first."
    fi
    
    for validator in "${VALIDATORS[@]}"; do
        if [ ! -d "$ASSETS_DIR/$validator" ]; then
            error "Validator directory not found: $ASSETS_DIR/$validator"
        fi
    done
}

# Create output directory
create_output_dir() {
    log_info "Creating output directory..."
    rm -rf "$OUTPUT_DIR"
    mkdir -p "$OUTPUT_DIR"
}

# Generate namespace manifest
generate_namespace() {
    log_info "Generating namespace manifest..."
    
    cat > "$OUTPUT_DIR/00-namespaces.yaml" << EOF
apiVersion: v1
kind: Namespace
metadata:
  name: $NAMESPACE
---
apiVersion: v1
kind: Namespace
metadata:
  name: consumer-chains
EOF
}

# Generate RBAC manifest
generate_rbac() {
    log_info "Generating RBAC manifest..."
    
    cat > "$OUTPUT_DIR/01-rbac.yaml" << EOF
apiVersion: v1
kind: ServiceAccount
metadata:
  name: monitor-sa
  namespace: $NAMESPACE
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: monitor-role
rules:
- apiGroups: [""]
  resources: ["namespaces"]
  verbs: ["get", "list", "watch", "create", "update", "patch"]
- apiGroups: [""]
  resources: ["services", "configmaps", "persistentvolumeclaims"]
  verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
- apiGroups: ["apps"]
  resources: ["deployments", "statefulsets"]
  verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
- apiGroups: [""]
  resources: ["pods"]
  verbs: ["get", "list", "watch", "delete"]
- apiGroups: [""]
  resources: ["pods/log"]
  verbs: ["get", "list"]
- apiGroups: [""]
  resources: ["events"]
  verbs: ["get", "list", "watch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: monitor-rolebinding
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: monitor-role
subjects:
- kind: ServiceAccount
  name: monitor-sa
  namespace: $NAMESPACE
EOF
}

# Generate genesis ConfigMap
generate_genesis_configmap() {
    log_info "Generating genesis ConfigMap..."
    
    local genesis_file="$ASSETS_DIR/alice/config/genesis.json"
    local node_ids_file="$ASSETS_DIR/node_ids.txt"
    
    # Build persistent peers string
    local peers=""
    while IFS=: read -r name node_id; do
        if [ -n "$peers" ]; then
            peers+=","
        fi
        peers+="${node_id}@validator-${name}:26656"
    done < "$node_ids_file"
    
    cat > "$OUTPUT_DIR/02-genesis-configmap.yaml" << EOF
apiVersion: v1
kind: ConfigMap
metadata:
  name: genesis-config
  namespace: $NAMESPACE
data:
  genesis.json: |
$(sed 's/^/    /' "$genesis_file")
  persistent_peers: "$peers"
EOF
}

# Generate validator ConfigMap
generate_validator_configmap() {
    local validator=$1
    local home_dir="$ASSETS_DIR/$validator"
    
    log_info "Generating ConfigMap for validator $validator..."
    
    # Get node ID
    local node_id
    node_id=$(grep "^$validator:" "$ASSETS_DIR/node_ids.txt" | cut -d: -f2)
    
    # Create config ConfigMap
    cat > "$OUTPUT_DIR/03-validator-${validator}-config.yaml" << EOF
apiVersion: v1
kind: ConfigMap
metadata:
  name: validator-${validator}-config
  namespace: $NAMESPACE
data:
  node_id: "$node_id"
  priv_validator_key.json: |
$(cat "$home_dir/config/priv_validator_key.json" | sed 's/^/    /')
  node_key.json: |
$(cat "$home_dir/config/node_key.json" | sed 's/^/    /')
EOF
}

# Generate validator keyring ConfigMap
generate_validator_keyring() {
    local validator=$1
    local home_dir="$ASSETS_DIR/$validator"
    local keyring_dir="$home_dir/keyring-test"
    
    log_info "Generating keyring ConfigMap for validator $validator..."
    
    cat > "$OUTPUT_DIR/04-validator-${validator}-keyring.yaml" << EOF
apiVersion: v1
kind: ConfigMap
metadata:
  name: validator-${validator}-keyring
  namespace: $NAMESPACE
binaryData:
EOF
    
    # Add each keyring file as base64 encoded data
    for file in "$keyring_dir"/*; do
        if [ -f "$file" ]; then
            local filename content
            filename=$(basename "$file")
            content=$(base64 < "$file" | tr -d '\n')
            echo "  $filename: $content" >> "$OUTPUT_DIR/04-validator-${validator}-keyring.yaml"
        fi
    done
}

# Generate validator deployment
generate_validator_deployment() {
    local validator=$1
    local index=$2
    
    log_info "Generating deployment for validator $validator..."
    
    cat > "$OUTPUT_DIR/05-validator-${validator}.yaml" << EOF
apiVersion: v1
kind: Service
metadata:
  name: validator-${validator}
  namespace: $NAMESPACE
spec:
  selector:
    app: validator
    validator: ${validator}
  ports:
  - port: 26656
    name: p2p
  - port: 26657
    name: rpc
  - port: 1317
    name: api
  - port: 9090
    name: grpc
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: validator-${validator}
  namespace: $NAMESPACE
spec:
  replicas: 1
  selector:
    matchLabels:
      app: validator
      validator: ${validator}
  template:
    metadata:
      labels:
        app: validator
        validator: ${validator}
        role: validator
    spec:
      terminationGracePeriodSeconds: 5
      initContainers:
      - name: init-validator
        image: ghcr.io/cosmos/interchain-security:v6.3.0
        command: ["/bin/sh", "-c"]
        args:
        - |
          # Initialize home directory structure
          interchain-security-pd init validator-${validator} --chain-id provider-1 --home /chain/.provider 2>/dev/null || true
          
          # Copy genesis
          cp /genesis/genesis.json /chain/.provider/config/genesis.json
          
          # Copy validator keys
          cp /validator-config/priv_validator_key.json /chain/.provider/config/
          cp /validator-config/node_key.json /chain/.provider/config/
          
          # Copy keyring
          mkdir -p /chain/.provider/keyring-test
          cp /keyring/* /chain/.provider/keyring-test/ || true
          
          # Get persistent peers excluding self
          PEERS=\$(cat /genesis/persistent_peers | sed "s/[^,@]*@validator-${validator}[^,]*//g" | sed 's/,,/,/g' | sed 's/^,//' | sed 's/,$//')
          
          # Update config
          sed -i "s/persistent_peers = \\"\\"/persistent_peers = \\"\$PEERS\\"/" /chain/.provider/config/config.toml
          sed -i 's/addr_book_strict = true/addr_book_strict = false/' /chain/.provider/config/config.toml
          
          # Update RPC to listen on all interfaces
          sed -i 's/127.0.0.1:26657/0.0.0.0:26657/' /chain/.provider/config/config.toml
          
          # Update gRPC to listen on all interfaces
          sed -i 's/localhost:9090/0.0.0.0:9090/' /chain/.provider/config/app.toml
          
          # Set proper permissions
          chown -R 1000:1000 /chain/.provider
        volumeMounts:
        - name: chain-data
          mountPath: /chain/.provider
        - name: genesis-config
          mountPath: /genesis
        - name: validator-config
          mountPath: /validator-config
        - name: keyring-data
          mountPath: /keyring
      containers:
      - name: validator
        image: ghcr.io/cosmos/interchain-security:v6.3.0
        command: ["interchain-security-pd", "start", "--home", "/chain/.provider"]
        ports:
        - containerPort: 26656
        - containerPort: 26657
        - containerPort: 1317
        - containerPort: 9090
        volumeMounts:
        - name: chain-data
          mountPath: /chain/.provider
        resources:
          requests:
            memory: "256Mi"
            cpu: "100m"
          limits:
            memory: "512Mi"
            cpu: "500m"
      volumes:
      - name: chain-data
        emptyDir: {}
      - name: genesis-config
        configMap:
          name: genesis-config
      - name: validator-config
        configMap:
          name: validator-${validator}-config
      - name: keyring-data
        configMap:
          name: validator-${validator}-keyring
EOF
    
    # Add NodePort for the first validator
    if [ "$validator" = "alice" ]; then
        cat >> "$OUTPUT_DIR/05-validator-${validator}.yaml" << EOF
---
apiVersion: v1
kind: Service
metadata:
  name: validator-${validator}-nodeport
  namespace: $NAMESPACE
spec:
  type: NodePort
  selector:
    app: validator
    validator: ${validator}
  ports:
  - port: 26657
    targetPort: 26657
    nodePort: 30657
    name: rpc
EOF
    fi
}

# Generate monitor deployment
generate_monitor_deployments() {
    log_info "Generating monitor deployments for each validator..."
    
    # Generate a monitor for each validator
    local monitor_index=6
    for validator in "${VALIDATORS[@]}"; do
        generate_single_monitor_deployment "$validator" "$monitor_index"
        monitor_index=$((monitor_index + 1))
    done
}

generate_single_monitor_deployment() {
    local validator=$1
    local index=$2
    
    log_info "Generating monitor deployment for $validator..."
    
    cat > "$OUTPUT_DIR/0${index}-monitor-${validator}.yaml" << EOF
apiVersion: apps/v1
kind: Deployment
metadata:
  name: monitor-${validator}
  namespace: $NAMESPACE
spec:
  replicas: 1
  selector:
    matchLabels:
      app: monitor
      validator: ${validator}
  template:
    metadata:
      labels:
        app: monitor
        validator: ${validator}
        role: monitor
    spec:
      terminationGracePeriodSeconds: 5
      serviceAccountName: monitor-sa
      initContainers:
      - name: wait-for-validator
        image: ghcr.io/cosmos/interchain-security:v6.3.0
        command: ["/bin/sh", "-c"]
        args:
        - |
          echo "Waiting for validator-${validator} to be ready..."
          until curl -sf http://validator-${validator}:26657/status > /dev/null 2>&1; do
            echo "Waiting for validator-${validator} to be ready..."
            sleep 5
          done
          echo "validator-${validator} is ready!"
      - name: init-keys
        image: ghcr.io/cosmos/interchain-security:v6.3.0
        command: ["/bin/sh", "-c"]
        args:
        - |
          # Copy validator keyring to monitor
          mkdir -p /data/.provider/keyring-test
          
          # Copy only this validator's keyring
          if [ -d /keyring-${validator} ]; then
            cp /keyring-${validator}/* /data/.provider/keyring-test/ || true
          fi
          
          # Set permissions
          chown -R 1000:1000 /data
        volumeMounts:
        - name: monitor-data
          mountPath: /data
        - name: keyring-${validator}
          mountPath: /keyring-${validator}
      containers:
      - name: monitor
        image: ics-monitor:latest
        imagePullPolicy: IfNotPresent
        command: 
        - "monitor"
        - "start"
        - "--node"
        - "http://validator-${validator}:26657"
        - "--chain-id"
        - "provider-1"
        - "--home"
        - "/data"
        - "--keyring-backend"
        - "test"
        - "--provider-endpoints"
        - "validator-alice:26656,validator-bob:26656,validator-charlie:26656"
        - "--from"
        - "${validator}"
        volumeMounts:
        - name: monitor-data
          mountPath: /data
        resources:
          requests:
            memory: "128Mi"
            cpu: "50m"
          limits:
            memory: "256Mi"
            cpu: "200m"
      volumes:
      - name: monitor-data
        emptyDir: {}
      - name: keyring-${validator}
        configMap:
          name: validator-${validator}-keyring
EOF
}

# Generate README
generate_readme() {
    log_info "Generating README..."
    
    cat > "$OUTPUT_DIR/README.md" << EOF
# Generated Kubernetes Manifests

These manifests were generated from the testnet assets created by \`testnet-coordinator.sh\`.

## Deployment Order

1. \`00-namespaces.yaml\` - Creates namespaces
2. \`01-rbac.yaml\` - Sets up RBAC for monitors
3. \`02-genesis-configmap.yaml\` - Genesis configuration
4. \`03-validator-*-config.yaml\` - Validator configs (P2P and consensus keys)
5. \`04-validator-*-keyring.yaml\` - Validator keyrings (account keys)
6. \`05-validator-*.yaml\` - Validator deployments
7. \`06-monitor-deployment.yaml\` - Monitor deployment

## Deploy Everything

\`\`\`bash
kubectl apply -f generated/
\`\`\`

## Generated from

- Assets directory: $ASSETS_DIR
- Genesis time: $(jq -r '.genesis_time' "$ASSETS_DIR/alice/config/genesis.json")
- Generated at: $(date)

## Validator Addresses

EOF
    
    while IFS=: read -r validator address; do
        echo "- $validator: $address" >> "$OUTPUT_DIR/README.md"
    done < "$ASSETS_DIR/validators.txt"
    
    echo "" >> "$OUTPUT_DIR/README.md"
    echo "## Node IDs" >> "$OUTPUT_DIR/README.md"
    echo "" >> "$OUTPUT_DIR/README.md"
    
    while IFS=: read -r validator node_id; do
        echo "- $validator: $node_id" >> "$OUTPUT_DIR/README.md"
    done < "$ASSETS_DIR/node_ids.txt"
}

# Main execution
main() {
    log_info "Starting Kubernetes manifest generation..."
    
    # Check prerequisites
    check_assets
    
    # Create output directory
    create_output_dir
    
    # Generate manifests
    generate_namespace
    generate_rbac
    generate_genesis_configmap
    
    # Generate per-validator manifests
    for i in "${!VALIDATORS[@]}"; do
        validator="${VALIDATORS[$i]}"
        generate_validator_configmap "$validator"
        generate_validator_keyring "$validator"
        generate_validator_deployment "$validator" "$i"
    done
    
    # Generate monitor deployment
    generate_monitor_deployments
    
    # Generate README
    generate_readme
    
    log_info "Kubernetes manifests generated successfully!"
    log_info "Output directory: $OUTPUT_DIR"
    log_info ""
    log_info "To deploy:"
    log_info "  kubectl apply -f $OUTPUT_DIR/"
}

# Show usage
usage() {
    echo "Usage: $0"
    echo ""
    echo "This script generates Kubernetes manifests from testnet assets."
    echo "Run testnet-coordinator.sh first to create the assets."
    echo ""
    echo "The script will:"
    echo "  - Read validator configs from k8s/testnet/assets/"
    echo "  - Generate Kubernetes manifests in k8s/testnet/generated/"
    echo "  - Include all necessary ConfigMaps and Deployments"
    exit 0
}

# Parse arguments
if [ "$1" == "-h" ] || [ "$1" == "--help" ]; then
    usage
fi

# Run main function
main