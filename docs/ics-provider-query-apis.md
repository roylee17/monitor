# ICS Provider Module Query APIs Reference

This document provides a comprehensive reference for all Interchain Security (ICS) provider module query APIs, including REST endpoints, CLI commands, and data structures.

## REST API Endpoints

All REST endpoints are prefixed with `/interchain_security/ccv/provider/`

### 1. Query Consumer Chains List

**Endpoint:** `GET /interchain_security/ccv/provider/consumer_chains/{phase}`

**CLI Command:** `query provider list-consumer-chains [phase]`

**Parameters:**
- `phase` (optional): Filter by consumer phase
  - `1` = REGISTERED
  - `2` = INITIALIZED
  - `3` = LAUNCHED
  - `4` = STOPPED
  - `5` = DELETED
- `pagination` (optional): Standard Cosmos SDK pagination

**Response:** List of consumer chains with details including:
- `chain_id`: The chain ID
- `client_id`: IBC client ID
- `consumer_id`: Unique consumer identifier
- `phase`: Current phase of the consumer chain
- `top_N`: Top N percentage (0 for opt-in chains)
- `validators_power_cap`: Maximum power cap for validators
- `validator_set_cap`: Maximum number of validators
- `min_stake`: Minimum stake requirement
- `allowlist`: Allowed validators list
- `denylist`: Denied validators list
- `metadata`: Chain metadata

### 2. Query Consumer Chain Details

**Endpoint:** `GET /interchain_security/ccv/provider/consumer_chain/{consumer_id}`

**CLI Command:** `query provider consumer-chain [consumer-id]`

**Parameters:**
- `consumer_id`: The consumer chain ID

**Response:** Detailed consumer chain information:
- `consumer_id`: Unique consumer identifier
- `chain_id`: The chain ID
- `owner_address`: Chain owner address
- `phase`: Current phase (REGISTERED/INITIALIZED/LAUNCHED/STOPPED/DELETED)
- `client_id`: IBC client ID (available after launch)
- `metadata`: Consumer chain metadata
- `init_params`: Initialization parameters including:
  - `initial_height`: Initial block height
  - `genesis_hash`: Hash of genesis state
  - `binary_hash`: Hash of binary
  - `spawn_time`: Time when chain should launch
  - `unbonding_period`: Consumer unbonding period
  - `ccv_timeout_period`: CCV packet timeout
  - `transfer_timeout_period`: Transfer packet timeout
  - `consumer_redistribution_fraction`: Token redistribution fraction
  - `blocks_per_distribution_transmission`: Blocks between distributions
  - `historical_entries`: Number of historical entries
- `power_shaping_params`: Power shaping parameters
- `infraction_parameters`: Slashing parameters

### 3. Query Consumer Genesis/CCV Patch

**Endpoint:** `GET /interchain_security/ccv/provider/consumer_genesis/{consumer_id}`

**CLI Command:** `query provider consumer-genesis [consumer-id]`

**Parameters:**
- `consumer_id`: The consumer chain ID

**Response:** Consumer genesis state (CCV patch) containing:
- `params`: Consumer chain parameters
- `provider`: Provider chain info
  - `client_state`: Provider client state
  - `consensus_state`: Provider consensus state
  - `initial_val_set`: Initial validator set with consumer keys
- `new_chain`: Whether this is a new chain

### 4. Query Opted-In Validators

**Endpoint:** `GET /interchain_security/ccv/provider/opted_in_validators/{consumer_id}`

**CLI Command:** `query provider consumer-opted-in-validators [consumer-id]`

**Parameters:**
- `consumer_id`: The consumer chain ID

**Response:**
- `validators_provider_addresses`: List of provider consensus addresses that have opted in

### 5. Query Assigned Consumer Keys

**Endpoint:** `GET /interchain_security/ccv/provider/validator_consumer_addr/{consumer_id}/{provider_address}`

**CLI Command:** `query provider validator-consumer-key [consumer-id] [provider-validator-address]`

**Parameters:**
- `consumer_id`: The consumer chain ID
- `provider_address`: Provider validator consensus address (e.g., cosmosvalcons1...)

**Response:**
- `consumer_address`: The assigned consumer chain address (if key assigned)

### 6. Query All Key Assignments for a Consumer

**Endpoint:** `GET /interchain_security/ccv/provider/address_pairs/{consumer_id}`

**CLI Command:** `query provider all-pairs-valconsensus-address [consumer-id]`

**Parameters:**
- `consumer_id`: The consumer chain ID

**Response:**
- `pair_val_con_addr`: Array of address pairs containing:
  - `provider_address`: Provider consensus address
  - `consumer_address`: Consumer consensus address
  - `consumer_key`: Consumer public key

### 7. Query Consumer Validators (Initial Validator Set)

**Endpoint:** `GET /interchain_security/ccv/provider/consumer_validators/{consumer_id}`

**CLI Command:** `query provider consumer-validators [consumer-id]`

**Parameters:**
- `consumer_id`: The consumer chain ID

**Response:**
- `validators`: Array of validators with:
  - `provider_address`: Provider consensus address
  - `consumer_key`: Consumer public key
  - `consumer_power`: Voting power on consumer
  - `consumer_commission_rate`: Commission on consumer
  - `provider_commission_rate`: Commission on provider
  - `description`: Validator description
  - `provider_operator_address`: Provider operator address
  - `jailed`: Jailed status
  - `status`: Bonding status
  - `provider_tokens`: Delegated tokens on provider
  - `provider_power`: Voting power on provider
  - `validates_current_epoch`: Whether validating current epoch

### 8. Query Consumer Genesis Time

**Endpoint:** `GET /interchain_security/ccv/provider/consumer_genesis_time/{consumer_id}`

**CLI Command:** `query provider consumer-genesis-time [consumer-id]`

**Parameters:**
- `consumer_id`: The consumer chain ID

**Response:**
- `genesis_time`: The genesis time of the consumer chain

### 9. Query Consumer Chains a Validator Must Validate

**Endpoint:** `GET /interchain_security/ccv/provider/consumer_chains_per_validator/{provider_address}`

**CLI Command:** `query provider consumer-chains-validator-has-to-validate [provider-address]`

**Parameters:**
- `provider_address`: Provider validator consensus address

**Response:**
- `consumer_ids`: List of consumer IDs the validator must validate

## Consumer Phase Values

```go
enum ConsumerPhase {
  CONSUMER_PHASE_UNSPECIFIED = 0;
  CONSUMER_PHASE_REGISTERED = 1;   // Chain registered but not initialized
  CONSUMER_PHASE_INITIALIZED = 2;  // Parameters set, waiting for spawn time
  CONSUMER_PHASE_LAUNCHED = 3;     // Chain is running
  CONSUMER_PHASE_STOPPED = 4;      // Chain has stopped
  CONSUMER_PHASE_DELETED = 5;      // Chain state deleted
}
```

## Example Usage

### Using CLI

```bash
# List all launched consumer chains
gaiad query provider list-consumer-chains 3

# Get specific consumer chain details
gaiad query provider consumer-chain 0

# Get consumer genesis (CCV patch)
gaiad query provider consumer-genesis 0

# Get opted-in validators
gaiad query provider consumer-opted-in-validators 0

# Get assigned consumer key for a validator
gaiad query provider validator-consumer-key 0 cosmosvalcons1...

# Get initial validator set
gaiad query provider consumer-validators 0
```

### Using REST API

```bash
# List all consumer chains
curl http://localhost:1317/interchain_security/ccv/provider/consumer_chains/

# Get specific consumer chain
curl http://localhost:1317/interchain_security/ccv/provider/consumer_chain/0

# Get consumer genesis
curl http://localhost:1317/interchain_security/ccv/provider/consumer_genesis/0

# Get opted-in validators
curl http://localhost:1317/interchain_security/ccv/provider/opted_in_validators/0
```

### Using gRPC

```bash
# List consumer chains
grpcurl -plaintext localhost:9090 interchain_security.ccv.provider.v1.Query/QueryConsumerChains

# Get consumer chain details
grpcurl -plaintext -d '{"consumer_id":"0"}' localhost:9090 interchain_security.ccv.provider.v1.Query/QueryConsumerChain

# Get consumer genesis
grpcurl -plaintext -d '{"consumer_id":"0"}' localhost:9090 interchain_security.ccv.provider.v1.Query/QueryConsumerGenesis
```

## Notes

1. **Consumer ID vs Chain ID**: 
   - `consumer_id` is a unique identifier assigned when the chain is registered
   - `chain_id` is the actual chain ID that will be used by the consumer chain

2. **Phase Transitions**:
   - REGISTERED → INITIALIZED: When initialization parameters are set
   - INITIALIZED → LAUNCHED: When spawn time is reached and validators have opted in
   - LAUNCHED → STOPPED: When chain is stopped
   - STOPPED → DELETED: When chain state is cleaned up

3. **Key Assignment**:
   - Validators can assign different consensus keys for consumer chains
   - If no key is assigned, the provider key is used by default
   - Assigned keys are included in the consumer genesis

4. **Validator Set**:
   - For Top-N chains: Top N% of provider validators by power
   - For Opt-In chains: Only validators who explicitly opted in
   - Power shaping parameters can modify the final validator set