# Configuration Reference

This document provides a comprehensive reference for configuring the Interchain Security Monitor system.

## Table of Contents

- [Configuration Files](#configuration-files)
- [Environment Variables](#environment-variables)
- [Monitor Configuration](#monitor-configuration)
- [Deployment Configuration](#deployment-configuration)
- [Network Configuration](#network-configuration)
- [Security Configuration](#security-configuration)
- [Performance Tuning](#performance-tuning)
- [Example Configurations](#example-configurations)

## Configuration Files

### Configuration Hierarchy

The monitor uses the following configuration precedence (highest to lowest):

1. Environment variables
2. Command-line flags
3. Configuration file (`config.yaml`)
4. Default values

### Main Configuration File

Location: `config.yaml` (or specified via `--config` flag)

```yaml
# Complete configuration reference
monitor:
  # Provider chain connection
  rpc_endpoints:
    - "tcp://validator-alice:26657"
    - "tcp://validator-bob:26657"    # Multiple for redundancy
    - "tcp://validator-charlie:26657"
  grpc_endpoint: "validator-alice:9090"
  
  # Validator identity
  validator_key: "alice"              # Key name in keyring
  validator_address: "cosmosvaloper..." # Optional, derived from key if not set
  
  # Chain configuration
  chain_id: "provider-1"
  
  # Working directory for state
  work_dir: "/var/lib/monitor"
  
  # Deployment configuration
  deployment:
    type: "kubernetes"                # "kubernetes" or "local"
    namespace: "provider"             # Kubernetes namespace
    
    # Kubernetes-specific settings
    kubernetes:
      in_cluster: true                # Use in-cluster config
      kubeconfig: ""                  # Path to kubeconfig (if not in-cluster)
      
      # Resource templates
      consumer_template: "consumer-template.yaml"
      service_template: "service-template.yaml"
      
      # Resource limits
      resources:
        consumer_chain:
          requests:
            memory: "512Mi"
            cpu: "500m"
          limits:
            memory: "2Gi"
            cpu: "2000m"
        hermes_relayer:
          requests:
            memory: "256Mi"
            cpu: "250m"
          limits:
            memory: "1Gi"
            cpu: "1000m"
    
    # Local deployment settings
    local:
      binary_path: "/usr/local/bin/interchain-security-cd"
      data_dir: "/var/lib/consumers"
      log_dir: "/var/log/consumers"

  # Event handling
  events:
    # WebSocket configuration
    websocket:
      enabled: true
      reconnect_interval: "5s"
      max_reconnect_attempts: 10
      ping_interval: "30s"
    
    # Event subscriptions
    subscriptions:
      - "tm.event='NewBlock'"         # For background sync
      - "create_consumer.consumer_id EXISTS"
      - "update_consumer.consumer_id EXISTS"
      - "remove_consumer.consumer_id EXISTS"
      - "opt_in.consumer_id EXISTS"
      - "opt_out.consumer_id EXISTS"
  
  # Polling configuration
  polling:
    background_sync_interval: "30s"    # General state sync
    spawn_check_interval: "5s"         # Pre-spawn monitoring
    rapid_poll_interval: "1s"          # 0-30s post-spawn
    active_poll_interval: "5s"         # 30s-2m post-spawn
    slow_poll_interval: "30s"          # 2m+ post-spawn
    cache_duration: "30s"              # Cache validity
  
  # Subnet management
  subnet:
    selection_percentage: 67           # Percentage of validators to select
    deterministic: true                # Use deterministic selection
    
    # Consumer chain defaults
    consumer_defaults:
      unbonding_period: "1814400s"     # 21 days
      ccv_timeout_period: "2419200s"   # 28 days
      transfer_timeout_period: "3600s" # 1 hour
      consumer_redistribution_fraction: "0.75"
      blocks_per_distribution_transmission: "1000"
      historical_entries: "10000"
  
  # Logging
  logging:
    level: "info"                      # debug, info, warn, error
    format: "json"                     # json or text
    output: "stdout"                   # stdout, stderr, or file path
    
    # Log rotation (if output is file)
    rotation:
      max_size: "100MB"
      max_backups: 3
      max_age: "7d"
      compress: true
  
  # Metrics
  metrics:
    enabled: true
    port: 8080
    path: "/metrics"
    
    # Metric labels
    labels:
      environment: "testnet"
      region: "us-east-1"
  
  # Health checks
  health:
    enabled: true
    port: 8081
    liveness_path: "/health/live"
    readiness_path: "/health/ready"
    
    # Readiness criteria
    readiness:
      require_chain_sync: true
      max_sync_lag_blocks: 10
```

## Environment Variables

All configuration options can be set via environment variables using the prefix `MONITOR_`:

### Core Settings

| Variable | Description | Default | Example |
|----------|-------------|---------|---------|
| `MONITOR_RPC_ENDPOINTS` | Comma-separated RPC endpoints | `tcp://localhost:26657` | `tcp://val1:26657,tcp://val2:26657` |
| `MONITOR_GRPC_ENDPOINT` | gRPC endpoint for queries | `localhost:9090` | `validator-alice:9090` |
| `MONITOR_CHAIN_ID` | Provider chain ID | Required | `provider-1` |
| `MONITOR_VALIDATOR_KEY` | Validator key name | Required | `alice` |
| `MONITOR_WORK_DIR` | Working directory | `/var/lib/monitor` | `/data/monitor` |

### Deployment Settings

| Variable | Description | Default | Example |
|----------|-------------|---------|---------|
| `MONITOR_DEPLOYMENT_TYPE` | Deployment type | `kubernetes` | `local` |
| `MONITOR_K8S_NAMESPACE` | Kubernetes namespace | `provider` | `cosmos-testnet` |
| `MONITOR_K8S_IN_CLUSTER` | Use in-cluster config | `true` | `false` |
| `MONITOR_K8S_KUBECONFIG` | Kubeconfig path | `~/.kube/config` | `/etc/kube/config` |

### Event Settings

| Variable | Description | Default | Example |
|----------|-------------|---------|---------|
| `MONITOR_WEBSOCKET_ENABLED` | Enable WebSocket events | `true` | `false` |
| `MONITOR_WEBSOCKET_RECONNECT_INTERVAL` | Reconnect interval | `5s` | `10s` |
| `MONITOR_POLLING_BACKGROUND_SYNC` | Background sync interval | `30s` | `1m` |
| `MONITOR_POLLING_SPAWN_CHECK` | Spawn check interval | `5s` | `3s` |

### Subnet Settings

| Variable | Description | Default | Example |
|----------|-------------|---------|---------|
| `MONITOR_SUBNET_SELECTION_PERCENTAGE` | Validator selection % | `67` | `50` |
| `MONITOR_SUBNET_DETERMINISTIC` | Use deterministic selection | `true` | `false` |

### Logging Settings

| Variable | Description | Default | Example |
|----------|-------------|---------|---------|
| `MONITOR_LOG_LEVEL` | Log level | `info` | `debug` |
| `MONITOR_LOG_FORMAT` | Log format | `json` | `text` |
| `MONITOR_LOG_OUTPUT` | Log output | `stdout` | `/var/log/monitor.log` |

### Metrics Settings

| Variable | Description | Default | Example |
|----------|-------------|---------|---------|
| `MONITOR_METRICS_ENABLED` | Enable metrics | `true` | `false` |
| `MONITOR_METRICS_PORT` | Metrics port | `8080` | `9090` |
| `MONITOR_METRICS_PATH` | Metrics path | `/metrics` | `/prometheus` |

## Monitor Configuration

### Multiple RPC Endpoints

Configure multiple RPC endpoints for redundancy:

```yaml
monitor:
  rpc_endpoints:
    - "tcp://validator-1:26657"
    - "tcp://validator-2:26657"
    - "tcp://validator-3:26657"
  
  # Failover behavior
  rpc_failover:
    strategy: "round-robin"    # round-robin, random, or sequential
    health_check_interval: "10s"
    timeout: "5s"
```

### Event Subscriptions

Customize event subscriptions:

```yaml
monitor:
  events:
    subscriptions:
      # Core ICS events (required)
      - "create_consumer.consumer_id EXISTS"
      - "update_consumer.consumer_id EXISTS"
      - "remove_consumer.consumer_id EXISTS"
      
      # Validator events (optional)
      - "opt_in.consumer_id EXISTS"
      - "opt_out.consumer_id EXISTS"
      - "set_consumer_key.consumer_id EXISTS"
      
      # Custom events
      - "message.action='/interchain_security.ccv.provider.v1.MsgCreateConsumer'"
```

## Deployment Configuration

### Kubernetes Resource Templates

Configure resource templates for consumer chain deployments:

```yaml
deployment:
  kubernetes:
    resources:
      consumer_chain:
        requests:
          memory: "1Gi"
          cpu: "1"
        limits:
          memory: "4Gi"
          cpu: "4"
        
        # Storage
        storage:
          class: "fast-ssd"
          size: "100Gi"
          
        # Node selection
        node_selector:
          node-type: "consumer-chain"
        
        # Tolerations
        tolerations:
          - key: "consumer-chain"
            operator: "Equal"
            value: "true"
            effect: "NoSchedule"
```

### Local Deployment

For local (non-Kubernetes) deployments:

```yaml
deployment:
  type: "local"
  local:
    binary_path: "/usr/local/bin/interchain-security-cd"
    
    # Process management
    process:
      supervisor: "systemd"    # systemd, supervisord, or none
      restart_policy: "always"
      restart_delay: "5s"
    
    # Directory structure
    directories:
      base: "/var/lib/ics"
      data: "${base}/data"
      logs: "${base}/logs"
      configs: "${base}/configs"
```

## Network Configuration

### Port Allocation

Configure deterministic port allocation:

```yaml
network:
  # Base ports
  base_ports:
    p2p: 26656
    rpc: 26657
    api: 1317
    grpc: 9090
    grpc_web: 9091
    prometheus: 26660
  
  # Consumer offset
  consumer_offset: 100
  
  # Port spacing between consumers
  port_spacing: 10
  
  # Port calculation: base + offset + (hash(chain_id) % 1000) * spacing
```

### Peer Discovery

Configure peer discovery settings:

```yaml
network:
  peer_discovery:
    # Method: "static", "dynamic", or "dns"
    method: "static"
    
    # Static peers (if method is "static")
    static_peers:
      provider:
        - "node-id@validator-alice:26656"
        - "node-id@validator-bob:26656"
    
    # DNS settings (if method is "dns")
    dns:
      domain: "cosmos.local"
      srv_record: "_p2p._tcp"
```

## Security Configuration

### Key Management

```yaml
security:
  keyring:
    backend: "test"             # test, file, os, or kwallet
    dir: "/var/lib/monitor/keys"
    
    # File backend settings
    file:
      password_file: "/etc/monitor/keyring-password"
    
    # Key rotation
    rotation:
      enabled: false
      interval: "30d"
```

### TLS Configuration

```yaml
security:
  tls:
    enabled: true
    
    # Client certificates for RPC
    client:
      cert: "/etc/monitor/tls/client.crt"
      key: "/etc/monitor/tls/client.key"
      ca: "/etc/monitor/tls/ca.crt"
    
    # Server certificates for metrics/health
    server:
      cert: "/etc/monitor/tls/server.crt"
      key: "/etc/monitor/tls/server.key"
```

### Access Control

```yaml
security:
  access_control:
    # IP allowlist for metrics endpoint
    metrics_allowlist:
      - "10.0.0.0/8"
      - "172.16.0.0/12"
    
    # Authentication for health endpoints
    health_auth:
      type: "basic"    # none, basic, or bearer
      username: "monitor"
      password_file: "/etc/monitor/health-password"
```

## Performance Tuning

### Connection Pooling

```yaml
performance:
  connections:
    # RPC connection pool
    rpc_pool:
      size: 10
      max_idle: 5
      idle_timeout: "5m"
    
    # gRPC settings
    grpc:
      max_message_size: "10MB"
      keepalive_interval: "30s"
```

### Caching

```yaml
performance:
  cache:
    # Consumer state cache
    consumer_state:
      size: 1000
      ttl: "30s"
    
    # Validator set cache
    validator_set:
      size: 100
      ttl: "5m"
```

### Batch Operations

```yaml
performance:
  batching:
    # Transaction batching
    transactions:
      enabled: true
      max_size: 10
      max_wait: "5s"
    
    # Query batching
    queries:
      enabled: true
      max_size: 50
      max_wait: "1s"
```

## Example Configurations

### Minimal Configuration

```yaml
# Minimal config for testing
monitor:
  rpc_endpoints: ["tcp://localhost:26657"]
  chain_id: "provider-1"
  validator_key: "alice"
```

### Production Configuration

```yaml
# Production configuration with redundancy
monitor:
  # Multiple RPC endpoints for failover
  rpc_endpoints:
    - "tcp://validator-1.cosmos.network:26657"
    - "tcp://validator-2.cosmos.network:26657"
    - "tcp://validator-3.cosmos.network:26657"
  
  grpc_endpoint: "validator-1.cosmos.network:9090"
  chain_id: "cosmoshub-4"
  validator_key: "validator-prod"
  
  # Production deployment settings
  deployment:
    type: "kubernetes"
    namespace: "cosmos-production"
    kubernetes:
      resources:
        consumer_chain:
          requests:
            memory: "2Gi"
            cpu: "2"
          limits:
            memory: "8Gi"
            cpu: "8"
  
  # Conservative polling for production
  polling:
    background_sync_interval: "1m"
    spawn_check_interval: "10s"
  
  # Production logging
  logging:
    level: "info"
    format: "json"
    output: "/var/log/monitor/monitor.log"
    rotation:
      max_size: "1GB"
      max_backups: 7
      max_age: "30d"
  
  # Metrics for monitoring
  metrics:
    enabled: true
    port: 9090
    labels:
      environment: "production"
      datacenter: "us-east-1"
```

### High-Security Configuration

```yaml
# High-security configuration
monitor:
  rpc_endpoints: ["tcp://validator.internal:26657"]
  chain_id: "secure-chain-1"
  validator_key: "validator-secure"
  
  # Security settings
  security:
    keyring:
      backend: "file"
      dir: "/secure/keys"
      file:
        password_file: "/secure/keyring-password"
    
    tls:
      enabled: true
      client:
        cert: "/secure/tls/client.crt"
        key: "/secure/tls/client.key"
        ca: "/secure/tls/ca.crt"
    
    access_control:
      metrics_allowlist:
        - "10.0.0.0/24"  # Only internal network
  
  # Restricted deployment
  deployment:
    kubernetes:
      resources:
        consumer_chain:
          # Security context
          security_context:
            run_as_non_root: true
            run_as_user: 1000
            fs_group: 1000
            read_only_root_filesystem: true
```

## Configuration Validation

The monitor validates configuration on startup:

```bash
# Validate configuration file
monitor validate-config --config config.yaml

# Test configuration with dry-run
monitor start --config config.yaml --dry-run
```

Common validation errors:

- Missing required fields (chain_id, validator_key)
- Invalid durations (must be like "30s", "5m", "1h")
- Invalid percentages (must be 0-100)
- Unreachable RPC endpoints
- Invalid file paths

## Best Practices

1. **Use environment variables** for sensitive values (keys, passwords)
2. **Configure multiple RPC endpoints** for redundancy
3. **Set appropriate resource limits** based on chain size
4. **Enable metrics and health checks** for monitoring
5. **Use structured logging** (JSON) in production
6. **Implement log rotation** to prevent disk fill
7. **Secure the keyring** with appropriate backend
8. **Tune polling intervals** based on network conditions
9. **Configure caching** to reduce RPC load
10. **Validate configuration** before deployment