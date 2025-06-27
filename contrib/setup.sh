#!/bin/bash

# set -eux

source util.sh

PD=interchain-security-pd
CD=interchain-security-cd

PROVIDER_CHAIN_ID=provider
CONSUMER_CHAIN_ID=subnet-1
# User balance of stake tokens
USER_COINS="100000000000stake"
# Amount of stake tokens staked
STAKE="100000000stake"
# Node IP address
NODE_IP="127.0.0.1"

# Home directory
HOME_DIR=gen

# Validator moniker
MONIKERS=("coordinator" "alice" "bob")
LEAD_VALIDATOR_MONIKER="coordinator"

# Hermes will connect to this node on both provider and consumer
HERMES_VALIDATOR_MONIKER=$LEAD_VALIDATOR_MONIKER
# HERMES_VALIDATOR_MONIKER="bob"

PROV_NODES_ROOT_DIR=${HOME_DIR}/nodes/provider
CONS_NODES_ROOT_DIR=${HOME_DIR}/nodes/consumer

# Base port. Ports assigned after these ports sequentially by nodes.
API_LADDR_BASEPORT=29160
RPC_LADDR_BASEPORT=29170
P2P_LADDR_BASEPORT=29180
GRPC_LADDR_BASEPORT=29190
NODE_ADDRESS_BASEPORT=29200
PPROF_LADDR_BASEPORT=29210
CLIENT_BASEPORT=29220

################################################################################
# Cleanup provider chains
################################################################################
function provider-cleanup() {
    pkill -f $PD &> /dev/null || true
    sleep 1
    rm -rf ${PROV_NODES_ROOT_DIR}
}

# Let lead validator create genesis file
LEAD_VALIDATOR_PROV_DIR=${PROV_NODES_ROOT_DIR}/provider-${LEAD_VALIDATOR_MONIKER}
LEAD_VALIDATOR_CONS_DIR=${CONS_NODES_ROOT_DIR}/consumer-${LEAD_VALIDATOR_MONIKER}
LEAD_PROV_KEY=${LEAD_VALIDATOR_MONIKER}-key
LEAD_PROV_LISTEN_ADDR=tcp://${NODE_IP}:${RPC_LADDR_BASEPORT}

################################################################################
# Init provider chains
################################################################################
function provider-init() {
    for index in "${!MONIKERS[@]}"
    do
        MONIKER=${MONIKERS[$index]}
        # validator key
        PROV_KEY=${MONIKER}-key

        # home directory of this validator on provider
        PROV_NODE_DIR=${PROV_NODES_ROOT_DIR}/provider-${MONIKER}

        # home directory of this validator on consumer
        CONS_NODE_DIR=${CONS_NODES_ROOT_DIR}/consumer-${MONIKER}

        # Build genesis file and node directory structure
        $PD init $MONIKER --chain-id $PROVIDER_CHAIN_ID --home ${PROV_NODE_DIR}

        jq ".app_state.gov.params.voting_period = \"10s\" \
            | .app_state.gov.params.expedited_voting_period = \"9s\" \
            | .app_state.staking.params.unbonding_time = \"86400s\" \
            | .app_state.provider.params.blocks_per_epoch = \"5\"" \
            ${PROV_NODE_DIR}/config/genesis.json > \
            ${PROV_NODE_DIR}/edited_genesis.json && mv ${PROV_NODE_DIR}/edited_genesis.json ${PROV_NODE_DIR}/config/genesis.json

        # Create account keypair
        $PD keys add $PROV_KEY --home ${PROV_NODE_DIR} --keyring-backend test --output json > ${PROV_NODE_DIR}/${PROV_KEY}.json 2>&1

        # copy genesis in, unless this validator is the lead validator
        if [ $MONIKER != $LEAD_VALIDATOR_MONIKER ]; then
            cp ${LEAD_VALIDATOR_PROV_DIR}/config/genesis.json ${PROV_NODE_DIR}/config/genesis.json
        fi

        # Add stake to user
        PROV_ACCOUNT_ADDR=$(jq -r '.address' ${PROV_NODE_DIR}/${PROV_KEY}.json)
        $PD genesis add-genesis-account $PROV_ACCOUNT_ADDR $USER_COINS --home ${PROV_NODE_DIR} --keyring-backend test

        # copy genesis out, unless this validator is the lead validator
        if [ $MONIKER != $LEAD_VALIDATOR_MONIKER ]; then
            cp ${PROV_NODE_DIR}/config/genesis.json ${LEAD_VALIDATOR_PROV_DIR}/config/genesis.json
        fi

        PPROF_LADDR=${NODE_IP}:$(($PPROF_LADDR_BASEPORT + $index))
        P2P_LADDR_PORT=$(($P2P_LADDR_BASEPORT + $index))

        # adjust configs of this node
        sed -i -r 's/timeout_commit = "5s"/timeout_commit = "3s"/g' ${PROV_NODE_DIR}/config/config.toml
        sed -i -r 's/timeout_propose = "3s"/timeout_propose = "1s"/g' ${PROV_NODE_DIR}/config/config.toml

        # make address book non-strict. necessary for this setup
        sed -i -r 's/addr_book_strict = true/addr_book_strict = false/g' ${PROV_NODE_DIR}/config/config.toml

        # avoid port double binding
        sed -i -r  "s/pprof_laddr = \"localhost:6060\"/pprof_laddr = \"${PPROF_LADDR}\"/g" ${PROV_NODE_DIR}/config/config.toml

        # allow duplicate IP addresses (all nodes are on the same machine)
        sed -i -r  's/allow_duplicate_ip = false/allow_duplicate_ip = true/g' ${PROV_NODE_DIR}/config/config.toml
    done
}

################################################################################
# Provider chains: gentx
################################################################################
function provider-gentx() {
    for MONIKER in "${MONIKERS[@]}"
    do
        # validator key
        PROV_KEY=${MONIKER}-key

        # home directory of this validator on provider
        PROV_NODE_DIR=${PROV_NODES_ROOT_DIR}/provider-${MONIKER}

        # copy genesis in, unless this validator is the lead validator
        if [ $MONIKER != $LEAD_VALIDATOR_MONIKER ]; then
            cp ${LEAD_VALIDATOR_PROV_DIR}/config/genesis.json* ${PROV_NODE_DIR}/config/genesis.json
        fi

        # Stake 1/1000 user's coins
        $PD genesis gentx $PROV_KEY $STAKE --chain-id $PROVIDER_CHAIN_ID --home ${PROV_NODE_DIR} --keyring-backend test --moniker $MONIKER
        sleep 1

        # Copy gentxs to the lead validator for possible future collection.
        # Obviously we don't need to copy the first validator's gentx to itself
        if [ $MONIKER != $LEAD_VALIDATOR_MONIKER ]; then
            cp ${PROV_NODE_DIR}/config/gentx/* ${LEAD_VALIDATOR_PROV_DIR}/config/gentx/
        fi
    done

    # Collect genesis transactions with lead validator
    $PD genesis collect-gentxs --home ${LEAD_VALIDATOR_PROV_DIR} --gentx-dir ${LEAD_VALIDATOR_PROV_DIR}/config/gentx/
}

################################################################################
# Provider chains: start
################################################################################

function provider-start() {
    for index in "${!MONIKERS[@]}"
    do
        MONIKER=${MONIKERS[$index]}

        PERSISTENT_PEERS=""

        for peer_index in "${!MONIKERS[@]}"
        do
            if [ $index == $peer_index ]; then
                continue
            fi
            PEER_MONIKER=${MONIKERS[$peer_index]}

            PEER_PROV_NODE_DIR=${PROV_NODES_ROOT_DIR}/provider-${PEER_MONIKER}

            PEER_NODE_ID=$($PD comet show-node-id --home ${PEER_PROV_NODE_DIR})

            PEER_P2P_LADDR_PORT=$(($P2P_LADDR_BASEPORT + $peer_index))
            PERSISTENT_PEERS="$PERSISTENT_PEERS,$PEER_NODE_ID@${NODE_IP}:${PEER_P2P_LADDR_PORT}"
        done

        # remove trailing comma from persistent peers
        PERSISTENT_PEERS=${PERSISTENT_PEERS:1}

        # validator key
        PROV_KEY=${MONIKER}-key

        # home directory of this validator on provider
        PROV_NODE_DIR=${PROV_NODES_ROOT_DIR}/provider-${MONIKER}

        # home directory of this validator on consumer
        CONS_NODE_DIR=${PROV_NODES_ROOT_DIR}/consumer-${MONIKER}

        # copy genesis in, unless this validator is already the lead validator and thus it already has its genesis
        if [ $MONIKER != $LEAD_VALIDATOR_MONIKER ]; then
            cp ${LEAD_VALIDATOR_PROV_DIR}/config/genesis.json ${PROV_NODE_DIR}/config/genesis.json
        fi

        API_LADDR_PORT=$(($API_LADDR_BASEPORT + $index))
        RPC_LADDR_PORT=$(($RPC_LADDR_BASEPORT + $index))
        P2P_LADDR_PORT=$(($P2P_LADDR_BASEPORT + $index))
        GRPC_LADDR_PORT=$(($GRPC_LADDR_BASEPORT + $index))
        NODE_ADDRESS_PORT=$(($NODE_ADDRESS_BASEPORT + $index))

        if [ $MONIKER == $HERMES_VALIDATOR_MONIKER ]; then
            PRPC_LADDR_PORT=$RPC_LADDR_PORT
            PGRPC_LADDR_PORT=$GRPC_LADDR_PORT
        fi

        $PD start \
            --home ${PROV_NODE_DIR} \
            --p2p.persistent_peers ${PERSISTENT_PEERS} \
            --rpc.laddr tcp://${NODE_IP}:${RPC_LADDR_PORT} \
            --api.address tcp://${NODE_IP}:${API_LADDR_PORT} \
            --grpc.address ${NODE_IP}:${GRPC_LADDR_PORT} \
            --address tcp://${NODE_IP}:${NODE_ADDRESS_PORT} \
            --p2p.laddr tcp://${NODE_IP}:${P2P_LADDR_PORT} \
            --api.enable=true \
            &> ${PROV_NODE_DIR}/logs &

    done
}

################################################################################
#  Send Create Consumer Message
################################################################################

function send-create-consumer() {

    SPAWN_TIME=$(date -v+20S -u +"%Y-%m-%dT%H:%M:%SZ")

    jq \
            "
                    .chain_id = \"$CONSUMER_CHAIN_ID\" |
                    .initialization_parameters.spawn_time = \"$SPAWN_TIME\"
            " \
            templ/create-consumer-msg.json > ${LEAD_VALIDATOR_PROV_DIR}/create-consumer-msg.json

    TX_RES=$($PD tx provider create-consumer \
        ${LEAD_VALIDATOR_PROV_DIR}/create-consumer-msg.json \
        --chain-id $PROVIDER_CHAIN_ID \
        --from $LEAD_PROV_KEY \
        --keyring-backend test \
        --home $LEAD_VALIDATOR_PROV_DIR \
        --node $LEAD_PROV_LISTEN_ADDR \
        -o json -y)

    sleep 5

    TX_RES=$($PD q tx --type=hash $(echo $TX_RES | jq -r '.txhash') \
        --home ${PROV_NODE_DIR} \
        --node tcp://${NODE_IP}:${RPC_LADDR_PORT} \
        -o json)

    if [ "$(echo $TX_RES | jq -r .code )" != "0" ]; then
        echo consumer creation failed with code: $(echo $TX_RES | jq .code )
        return 1
    fi

    CONSUMER_ID="$(echo $TX_RES | jq -r '.events[].attributes[] | select(.key == "consumer_id") | .value')"

    if [ "$CONSUMER_ID" == "" ]; then
        echo "consumer creation event not found"
        return 1
    fi

    echo "consumer created: $CONSUMER_ID"
}

################################################################################
# Cleanup consumer chains
################################################################################
function consumer-cleanup() {
    # # Clean start
    pkill -f $CD &> /dev/null || true
    sleep 1
    rm -rf ${CONS_NODES_ROOT_DIR}
}

################################################################################
# Init consumer chains
################################################################################
function consumer-init() {
    for index in "${!MONIKERS[@]}"
    do
        MONIKER=${MONIKERS[$index]}
        # validator key
        PROV_KEY=${MONIKER}-key

        # home directory of this validator on consumer
        CONS_NODE_DIR=${CONS_NODES_ROOT_DIR}/consumer-${MONIKER}

        # Build genesis file and node directory structure
        $CD init $MONIKER --chain-id $CONSUMER_CHAIN_ID  --home ${CONS_NODE_DIR}

        # Create account keypair
        $CD keys add $PROV_KEY --home  ${CONS_NODE_DIR} --keyring-backend test --output json > ${CONS_NODE_DIR}/${PROV_KEY}.json 2>&1

        # Add stake to user
        CONS_ACCOUNT_ADDR=$(jq -r '.address' ${CONS_NODE_DIR}/${PROV_KEY}.json)
        $CD genesis add-genesis-account $CONS_ACCOUNT_ADDR $USER_COINS --home ${CONS_NODE_DIR}

        jq \
                "
                        .genesis_time = \"$SPAWN_TIME\"
                " \
                ${CONS_NODE_DIR}/config/genesis.json > ${CONS_NODE_DIR}/config/pre-ccv-genesis.json

        # copy genesis in, unless this validator is the lead validator
        if [ $MONIKER != $LEAD_VALIDATOR_MONIKER ]; then
            cp ${LEAD_VALIDATOR_CONS_DIR}/config/genesis.json ${CONS_NODE_DIR}/config/genesis.json
        fi

        PPROF_LADDR=${NODE_IP}:$(($PPROF_LADDR_BASEPORT + ${#MONIKERS[@]} + $index))
        P2P_LADDR_PORT=$(($P2P_LADDR_BASEPORT + ${#MONIKERS[@]} + $index))

        # adjust configs of this node
        sed -i -r 's/timeout_commit = "5s"/timeout_commit = "3s"/g' ${CONS_NODE_DIR}/config/config.toml
        sed -i -r 's/timeout_propose = "3s"/timeout_propose = "1s"/g' ${CONS_NODE_DIR}/config/config.toml

        # make address book non-strict. necessary for this setup
        sed -i -r 's/addr_book_strict = true/addr_book_strict = false/g' ${CONS_NODE_DIR}/config/config.toml

        # avoid port double binding
        sed -i -r  "s/pprof_laddr = \"localhost:6060\"/pprof_laddr = \"${PPROF_LADDR}\"/g" ${CONS_NODE_DIR}/config/config.toml

        # allow duplicate IP addresses (all nodes are on the same machine)
        sed -i -r  's/allow_duplicate_ip = false/allow_duplicate_ip = true/g' ${CONS_NODE_DIR}/config/config.toml

        # Set default client port
        CLIENT_PORT=$(($CLIENT_BASEPORT + ${#MONIKERS[@]} + $index))
        sed -i -r "/node =/ s/= .*/= \"tcp:\/\/${NODE_IP}:${CLIENT_PORT}\"/" ${CONS_NODE_DIR}/config/client.toml

    done
}

################################################################################
# Opt-in to consumer chain
################################################################################
function provider-opt-in() {
    for index in "${!MONIKERS[@]}"
    do
        MONIKER=${MONIKERS[$index]}

        # validator key
        PROV_KEY=${MONIKER}-key

        # home directory of this validator on provider
        PROV_NODE_DIR=${PROV_NODES_ROOT_DIR}/provider-${MONIKER}

        # home directory of this validator on consumer
        CONS_NODE_DIR=${CONS_NODES_ROOT_DIR}/consumer-${MONIKER}

        RPC_LADDR_PORT=$(($RPC_LADDR_BASEPORT + $index))

        CONSUMER_VALIDATOR_PUBKEY=$($CD comet show-validator --home ${CONS_NODE_DIR})
        $PD tx provider opt-in $CONSUMER_ID $CONSUMER_VALIDATOR_PUBKEY \
            --home ${PROV_NODE_DIR} \
            --chain-id $PROVIDER_CHAIN_ID \
            --from $PROV_KEY \
            --keyring-backend test \
            --node tcp://${NODE_IP}:${RPC_LADDR_PORT} \
            -o json -y | jq

        sleep 5

        $PD tx provider assign-consensus-key $CONSUMER_ID $CONSUMER_VALIDATOR_PUBKEY \
            --home ${PROV_NODE_DIR} \
            --from $PROV_KEY \
            --keyring-backend test \
            --node tcp://${NODE_IP}:${RPC_LADDR_PORT} \
            -o json -y | jq
    done
}


################################################################################
# List opted-in validators
################################################################################
function list-opted-in-validators() {
    $PD q provider consumer-opted-in-validators $CONSUMER_ID \
        --node $LEAD_PROV_LISTEN_ADDR
}

################################################################################
# Generate CCV genesis
################################################################################
function generate-ccv-genesis() {
    $PD q provider consumer-genesis $CONSUMER_ID \
        --node $LEAD_PROV_LISTEN_ADDR \
        -o json | jq > gen/ccv.json

    jq -s \
            "
                    .[0].app_state.ccvconsumer = .[1] |
                    .[0]
            " \
            ${LEAD_VALIDATOR_CONS_DIR}/config/genesis.json gen/ccv.json > gen/genesis.json

    cp gen/genesis.json ${LEAD_VALIDATOR_CONS_DIR}/config/genesis.json
}

################################################################################
# Start consumer chains
################################################################################
function consumer-start() {

    for index in "${!MONIKERS[@]}"
    do
        MONIKER=${MONIKERS[$index]}

        PERSISTENT_PEERS=""

        for peer_index in "${!MONIKERS[@]}"
        do
            if [ $index == $peer_index ]; then
                continue
            fi
            PEER_MONIKER=${MONIKERS[$peer_index]}

            PEER_CONS_NODE_DIR=${CONS_NODES_ROOT_DIR}/consumer-${PEER_MONIKER}

            PEER_NODE_ID=$($PD comet show-node-id --home ${PEER_CONS_NODE_DIR})

            PEER_P2P_LADDR_PORT=$(($P2P_LADDR_BASEPORT + ${#MONIKERS[@]} + $peer_index))
            PERSISTENT_PEERS="$PERSISTENT_PEERS,$PEER_NODE_ID@${NODE_IP}:${PEER_P2P_LADDR_PORT}"
        done

        # remove trailing comma from persistent peers
        PERSISTENT_PEERS=${PERSISTENT_PEERS:1}

        # validator key
        PROV_KEY=${MONIKER}-key

        # home directory of this validator on provider
        PROV_NODE_DIR=${PROV_NODES_ROOT_DIR}/provider-${MONIKER}

        # home directory of this validator on consumer
        CONS_NODE_DIR=${CONS_NODES_ROOT_DIR}/consumer-${MONIKER}

        # copy genesis in, unless this validator is already the lead validator and thus it already has its genesis
        if [ $MONIKER != $LEAD_VALIDATOR_MONIKER ]; then
            cp ${LEAD_VALIDATOR_CONS_DIR}/config/genesis.json ${CONS_NODE_DIR}/config/genesis.json
            continue
        fi

        RPC_LADDR_PORT=$(($RPC_LADDR_BASEPORT + ${#MONIKERS[@]} + $index))
        P2P_LADDR_PORT=$(($P2P_LADDR_BASEPORT + ${#MONIKERS[@]} + $index))
        GRPC_LADDR_PORT=$(($GRPC_LADDR_BASEPORT + ${#MONIKERS[@]} + $index))
        NODE_ADDRESS_PORT=$(($NODE_ADDRESS_BASEPORT + ${#MONIKERS[@]} + $index))

        if [ $MONIKER == $HERMES_VALIDATOR_MONIKER ]; then
            CRPC_LADDR_PORT=$RPC_LADDR_PORT
            CGRPC_LADDR_PORT=$GRPC_LADDR_PORT
        fi

        $CD start \
            --home ${CONS_NODE_DIR} \
            --p2p.persistent_peers ${PERSISTENT_PEERS} \
            --rpc.laddr tcp://${NODE_IP}:${RPC_LADDR_PORT} \
            --grpc.address ${NODE_IP}:${GRPC_LADDR_PORT} \
            --address tcp://${NODE_IP}:${NODE_ADDRESS_PORT} \
            --p2p.laddr tcp://${NODE_IP}:${P2P_LADDR_PORT} \
            --grpc-web.enable=false &> ${CONS_NODE_DIR}/logs &

        sleep 6
    done
}
################################################################################
# Hermes
################################################################################

HERMES_PROV_NODE_DIR=${PROV_NODES_ROOT_DIR}/provider-${HERMES_VALIDATOR_MONIKER}
HERMES_KEY=${HERMES_VALIDATOR_MONIKER}-key
HERMES_CONS_NODE_DIR=${CONS_NODES_ROOT_DIR}/consumer-${HERMES_VALIDATOR_MONIKER}

function hermes-init() {

    tee ~/.hermes/config.toml<<EOF
[global]
log_level = "info"

[mode]

[mode.clients]
enabled = true
refresh = true
misbehaviour = true

[mode.connections]
enabled = false

[mode.channels]
enabled = false

[mode.packets]
enabled = true

        [[chains]]
        account_prefix = "cosmos"
        clock_drift = "5s"
        gas_multiplier = 1.1
        grpc_addr = "tcp://${NODE_IP}:${PGRPC_LADDR_PORT}"
        id = "provider"
        key_name = "$HERMES_VALIDATOR_MONIKER"
        max_gas = 20000000
        rpc_addr = "http://${NODE_IP}:${PRPC_LADDR_PORT}"
        rpc_timeout = "10s"
        store_prefix = "ibc"
        trusting_period = "14days"
        event_source = { mode = 'push', url = 'ws://${NODE_IP}:${PRPC_LADDR_PORT}/websocket' , batch_delay = '50ms' }
        ccv_consumer_chain = false

        [chains.gas_price]
                denom = "stake"
                price = 0.000

        [chains.trust_threshold]
                denominator = "3"
                numerator = "1"



        [[chains]]
        account_prefix = "consumer"
        clock_drift = "5s"
        gas_multiplier = 1.1
        grpc_addr = "tcp://${NODE_IP}:${CGRPC_LADDR_PORT}"
        id = "${CONSUMER_CHAIN_ID}"
        key_name = "$HERMES_VALIDATOR_MONIKER"
        max_gas = 20000000
        rpc_addr = "http://${NODE_IP}:${CRPC_LADDR_PORT}"
        rpc_timeout = "10s"
        store_prefix = "ibc"
        trusting_period = "14days"
        event_source = { mode = 'push', url = 'ws://${NODE_IP}:${CRPC_LADDR_PORT}/websocket' , batch_delay = '50ms' }
        ccv_consumer_chain = true

        [chains.gas_price]
                denom = "stake"
                price = 0.000

        [chains.trust_threshold]
                denominator = "3"
                numerator = "1"
EOF

    # Delete all previous keys in relayer
    hermes keys delete --chain $CONSUMER_CHAIN_ID --all
    hermes keys delete --chain $PROVIDER_CHAIN_ID --all

    # Restore keys to hermes relayer
    hermes keys add --key-file  ${HERMES_PROV_NODE_DIR}/${HERMES_KEY}.json --chain $PROVIDER_CHAIN_ID
    hermes keys add --key-file  ${HERMES_CONS_NODE_DIR}/${HERMES_KEY}.json --chain $CONSUMER_CHAIN_ID

}

################################################################################
# Hermes create connection and channel
################################################################################
function hermes-create-connection() {
    hermes create connection \
        --a-chain $CONSUMER_CHAIN_ID \
        --a-client 07-tendermint-0 \
        --b-client 07-tendermint-0
}

function hermes-create-channel() {
    hermes create channel \
        --a-chain $CONSUMER_CHAIN_ID \
        --a-port consumer \
        --b-port provider \
        --order ordered \
        --channel-version 1 \
        --a-connection connection-0
}

################################################################################
# Hermes
################################################################################
function hermes-test() {
    # Delegate tokens to validator bob and relay the resulting VSC packet to consumer
    PROV_VAL_ADDR=$($PD q staking delegations $PROV_ACCOUNT_ADDR \
        --node tcp://${NODE_IP}:${RPC_LADDR_BASEPORT} \
        -o json | jq -r '.delegation_responses[0].delegation.validator_address')

    $PD tx staking delegate $PROV_VAL_ADDR 1000000stake \
        --from $PROV_KEY \
        --keyring-backend test \
        --chain-id $PROVIDER_CHAIN_ID \
        --home ${PROV_NODE_DIR} \
        --node tcp://${NODE_IP}:${RPC_LADDR_BASEPORT} \
        -y

    ## wait enough for an epoch to elapse
    sleep 15

    hermes clear packets  --chain $PROVIDER_CHAIN_ID --port provider --channel channel-0

    sleep 3
}

function ics-test() {

    ## provider and consumer should share the same valset
    PROV_VOTING_POWER=$($PD q comet-validator-set \
        --node tcp://${NODE_IP}:${RPC_LADDR_BASEPORT} \
        -o json | jq -r '.validators[].voting_power' | tr '\n' ' ')

    echo "provider voting power: $PROV_VOTING_POWER"

    CONSU_VOTING_POWER=$($CD q comet-validator-set \
        --node tcp://${NODE_IP}:${CRPC_LADDR_PORT} \
        -o json | jq -r '.validators[].voting_power' | tr '\n' ' ')

    echo "consumer voting power: $CONSU_VOTING_POWER"

    if [ "$PROV_VOTING_POWER" != "$CONSU_VOTING_POWER" ]; then
        echo "the provider and consumer validator set should be the same\
        -- verify that the CCV channel was correctly established"
        exit 1
    fi

    echo "consumer $CONSUMER_ID launched and CCV channel established!"
}
