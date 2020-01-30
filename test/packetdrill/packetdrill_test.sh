#!/bin/bash
set -euxo pipefail

# Copyright 2018 The gVisor Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# Run a packetdrill test.  Two docker containers are made, one for the
# Device-Under-Test (DUT) and one for the test runner.  Each is attached with
# two networks, one for control packets that aid the test and one for test
# packets which are sent as part of the test and observed for correctness.
LONGOPTS=runtime:,dut_platform:,init_script:

PARSED=$(getopt --options "" --longoptions=$LONGOPTS --name "$0" -- "$@")
if [[ ${PIPESTATUS[0]} -ne 0 ]]; then
  # getopt has complained about wrong arguments to stdout
  exit 2
fi
echo "Args are: $PARSED"

eval set -- "$PARSED"

while true; do
  case "$1" in
    --dut_platform)
      declare -r DUT_PLATFORM="$2"
      shift 2
      ;;
    --init_script)
      declare -r INIT_SCRIPT="$2"
      shift 2
      ;;
    --runtime)
      declare -r RUNTIME="--runtime=$2"
      shift 2
      ;;
    --)
      shift
      break
      ;;
    *)
      echo "Programming error"
      exit 3
  esac
done

# All the other arguments are scripts.
declare -r scripts="$@"

if [[ "${DUT_PLATFORM}" == "netstack" ]]; then
  declare -r RUNTIME="--runtime runsc-d"
elif [[ "${DUT_PLATFORM}" == "linux" ]]; then
  declare -r RUNTIME=""
else
  echo "Bad or missing --dut_platform argument: ${DUT_PLATFORM}"
fi
declare -r PACKETDRILL="/packetdrill/gtests/net/packetdrill/packetdrill"
# Use random numbers so that test networks don't collide.
declare -r CTRL_NET="ctrl_net-${RANDOM}${RANDOM}"
declare -r TEST_NET="test_net-${RANDOM}${RANDOM}"
declare -r tolerance_usecs=100000
# On both DUT and test runner, testing packets are on the eth2 interface.
declare -r TEST_DEVICE="eth2"
# Number of bits in the *_NET_PREFIX variables.
declare -r NET_MASK="24"
function new_net_prefix() {
  # Class C, 192.0.0.0 to 223.255.255.255, transitionally has mask 24.
  echo "$(shuf -i 192-223 -n 1).$(shuf -i 0-255 -n 1).$(shuf -i 0-255 -n 1)"
}
# Last bits of the DUT's IP address.
declare -r DUT_NET_SUFFIX=".10"
# Control port
declare -r CTRL_PORT="40000"
# Last bits of the test runner's IP address.
declare -r TEST_RUNNER_NET_SUFFIX=".20"
declare -r TIMEOUT="3000"

# Make sure that docker is installed.
docker --version

# Subnet for control packets between test runner and DUT.
declare CTRL_NET_PREFIX=$(new_net_prefix)
while ! docker network create "--subnet=${CTRL_NET_PREFIX}.0/${NET_MASK}" "${CTRL_NET}"; do
  sleep 0.1
  declare CTRL_NET_PREFIX=$(new_net_prefix)
done

# Subnet for the packets that are part of the test.
declare TEST_NET_PREFIX=$(new_net_prefix)
while ! docker network create "--subnet=${TEST_NET_PREFIX}.0/${NET_MASK}" "${TEST_NET}"; do
  sleep 0.1
  declare TEST_NET_PREFIX=$(new_net_prefix)
done

function finish {
  if [[ $? -ne 0 ]]; then
    echo FAIL: Previous command failed.
  else
    echo PASS: Exiting without failures.
  fi
  for net in $(echo "${CTRL_NET} ${TEST_NET}"); do
    # Kill all processes attached to ${net}.
    (docker network inspect "${net}" --format '{{range $key, $value := .Containers}}{{$key}},{{end}}' | xargs --delimiter=, docker kill) || true
    # Remove the network.
    docker network rm "${net}" || true
  done
}
trap finish EXIT

# TODO: Put an image in gcr.io that is built like this:
# FROM ubuntu
#
# RUN apt-get update
# RUN apt-get install -y net-tools git iptables iputils-ping netcat tcpdump jq tar
# RUN hash -r
# RUN git clone https://github.com/google/packetdrill.git
# RUN cd packetdrill/gtests/net/packetdrill && ./configure && apt-get install -y bison flex make && make
declare -r IMAGE_TAG="eyal0/gvisor"
docker pull "${IMAGE_TAG}"

# Create the DUT container and connect to network.
DUT=`docker create ${RUNTIME} --privileged --rm --stop-timeout ${TIMEOUT} -it ${IMAGE_TAG}`
docker network connect "${CTRL_NET}" --ip "${CTRL_NET_PREFIX}${DUT_NET_SUFFIX}" "${DUT}"
docker network connect "${TEST_NET}" --ip "${TEST_NET_PREFIX}${DUT_NET_SUFFIX}" "${DUT}"
docker start "${DUT}"

# Create the test runner container and connect to network.
TEST_RUNNER=`docker create --privileged --rm --stop-timeout ${TIMEOUT} -it ${IMAGE_TAG}`
docker network connect "${CTRL_NET}" --ip "${CTRL_NET_PREFIX}${TEST_RUNNER_NET_SUFFIX}" "${TEST_RUNNER}"
docker network connect "${TEST_NET}" --ip "${TEST_NET_PREFIX}${TEST_RUNNER_NET_SUFFIX}" "${TEST_RUNNER}"
docker start "${TEST_RUNNER}"

docker cp -L "${INIT_SCRIPT}" "${DUT}:packetdrill_setup.sh"

docker exec -t ${TEST_RUNNER} tcpdump -U -n -i "${TEST_DEVICE}" not port "${CTRL_PORT}" &

docker exec -d "${TEST_RUNNER}" \
  ${PACKETDRILL} --wire_server --wire_server_dev="${TEST_DEVICE}" \
    --wire_server_ip="${CTRL_NET_PREFIX}${TEST_RUNNER_NET_SUFFIX}" --wire_server_port="${CTRL_PORT}" \
    --local_ip="${TEST_NET_PREFIX}${TEST_RUNNER_NET_SUFFIX}" \
    --remote_ip="${TEST_NET_PREFIX}${DUT_NET_SUFFIX}"

# Because the Linux kernel receives the SYN-ACK but didn't send the SYN it will
# issue a RST. To prevent this IPtables can be used to filter those out.
docker exec "${TEST_RUNNER}" \
  iptables -A OUTPUT -p tcp --tcp-flags RST RST -j DROP

while ! docker exec "${DUT}" nc -zv "${CTRL_NET_PREFIX}${TEST_RUNNER_NET_SUFFIX}" "${CTRL_PORT}"; do
  sleep 0.1
done

declare -a dut_scripts
for script in $scripts; do
  docker cp -L "${script}" "${DUT}:$(basename ${script})"
  dut_scripts+="/$(basename ${script})"
done

docker exec -t "${DUT}" \
  ${PACKETDRILL} --wire_client --wire_client_dev="${TEST_DEVICE}" \
    --wire_server_ip="${CTRL_NET_PREFIX}${TEST_RUNNER_NET_SUFFIX}" --wire_server_port="${CTRL_PORT}" \
    --local_ip="${TEST_NET_PREFIX}${DUT_NET_SUFFIX}" \
    --remote_ip="${TEST_NET_PREFIX}${TEST_RUNNER_NET_SUFFIX}" \
    --init_scripts=/packetdrill_setup.sh \
    --tolerance_usecs="${tolerance_usecs}" "${dut_scripts}"
