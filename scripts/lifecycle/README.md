# Consumer Chain Lifecycle Scripts

This directory contains scripts for managing the lifecycle of consumer chains in Interchain Security.

## Scripts

### Core Scripts

- **`create-consumer.sh`** - Creates a new consumer chain

  ```bash
  # Create with auto-generated chain ID and 30s spawn delay
  ./create-consumer.sh

  # Create with specific chain ID and 120s spawn delay
  ./create-consumer.sh -c mychain -s 120

  # Create with auto opt-in
  ./create-consumer.sh -o
  ```

- **`update-consumer.sh`** - Updates a consumer chain's parameters

  ```bash
  # Display current consumer info
  ./update-consumer.sh 0

  # Update spawn time to 120 seconds from now
  ./update-consumer.sh 0 -d 120

  # Update metadata
  ./update-consumer.sh 0 -n "New Name" -D "New Description"
  ```

- **`remove-consumer.sh`** - Removes a consumer chain

  ```bash
  # Remove consumer chain by ID
  ./remove-consumer.sh 0
  ```

### Utility Scripts

- **`list-consumers.sh`** - List consumer chains with various display options

  ```bash
  ./list-consumers.sh              # List all consumer chains
  ./list-consumers.sh -d           # List all chains with details
  ./list-consumers.sh -i           # Show summary information
  ./list-consumers.sh 0            # Show details for consumer 0
  ```

- **`consumer-utils.sh`** - Common utility functions and configuration for all lifecycle scripts
  - Contains all shared functions for querying, transactions, display, and validation
  - Provides consistent configuration defaults (namespace, validator, etc.)

### Monitoring Scripts

- **`monitor-phase.sh`** - Monitor consumer chain phase transitions

  ```bash
  # Show current phase
  ./monitor-phase.sh 0

  # Continuously monitor phase changes
  ./monitor-phase.sh -c 0
  ```

## Consumer Chain Phases

Consumer chains in ICS v7 go through the following phases:

1. **REGISTERED** - Chain is registered but not yet initialized
2. **INITIALIZED** - Spawn time is set, waiting for launch
3. **LAUNCHED** - Chain is running
4. **STOPPED** - Chain has been stopped
5. **DELETED** - Chain has been deleted after unbonding

## Usage Examples

### Creating a Consumer Chain

```bash
# Basic creation with 60 second spawn delay
./create-consumer.sh -s 60

# Create with specific chain ID
./create-consumer.sh -c devnet-1 -s 120

# Create with auto opt-in (all validators)
./create-consumer.sh -o -s 30
```

### Checking Consumer Status

```bash
# Show all consumer chains
./list-consumers.sh

# Show summary information
./list-consumers.sh -i

# Check specific consumer
./list-consumers.sh 0
```

## Integration with Monitor

The Interchain Security Monitor watches for consumer chain events and automatically:

- Detects new consumer chains from `create_consumer` events
- Deploys consumer chains when they reach LAUNCHED phase
- Manages consumer chain lifecycle based on phase changes
