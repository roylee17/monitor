#! /bin/bash

source setup.sh

provider-cleanup
provider-init
sleep 1
provider-gentx
sleep 1
provider-start
sleep 5

send-create-consumer

consumer-cleanup

consumer-init
provider-opt-in
list-opted-in-validators

wait_until $SPAWN_TIME

sleep 10
generate-ccv-genesis
consumer-start

sleep 5

hermes-init
hermes-create-connection
hermes-create-channel

sleep 5
hermes-test
ics-test

interchain-security-pd q comet-validator-set --node tcp://127.0.0.1:29170 -o json |jq -r '.validators'
interchain-security-cd q comet-validator-set --node tcp://127.0.0.1:29173 -o json |jq -r '.validators'

