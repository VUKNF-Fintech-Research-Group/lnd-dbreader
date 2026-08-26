#!/usr/bin/env bash
############################################################
#  [*] lnd — container entrypoint for lnd-dbreader-lnd
#
#  Adapted from lnd's own docker/lnd/start-lnd.sh. Turns
#  the compose environment into one `exec lnd ...` line for
#  a graph-only node: neutrino backend (no full bitcoin
#  node), no seed backup (the wallet never holds funds —
#  the node exists so dbreader can read the gossip graph it
#  collects) and a FIXED peer list instead of DNS seeds.
#
#  Runs as user 1000 on a read-only root filesystem, so
#  the data directory cannot be the image's /root/.lnd
#  (/root is 0700 root and unreachable): compose mounts
#  _DATA/lnd at /lnd and --lnddir points lnd there. lncli
#  in the healthcheck needs the same --lnddir=/lnd.
#
#  Environment (compose sets the first four):
#    NETWORK          — mainnet
#    NEUTRINO_CONNECT — comma list of bitcoin peers; these
#                       are the ONLY peers the node talks
#                       to, so every one must serve compact
#                       filters (neutrinoChecker.py checks)
#    FEE_URL          — external fee estimator, mandatory
#                       for neutrino on mainnet
#    LNDHOST          — extra TLS SAN for the RPC cert
#    LND_DEBUG        — lnd debuglevel (default debug)
#    CHAIN            — bitcoin (default bitcoin)
#    LNDDIR           — the data directory (default /lnd)
#
#  Mounted read-only at /start-lnd.sh and set as the
#  service's entrypoint in docker-compose.yml; anything in
#  the service's `command:` is appended to the lnd line.
############################################################


# Any failing command ends the script — including a
# set_default assignment whose subshell called error
set -e








############################################################
# error
############################################################
#
# Message to stderr, exit 1. Only ever called from inside a
# $(...) substitution (set_default), so the exit ends that
# subshell alone — it is `set -e` on the failed assignment
# that actually stops the script.
#
# Used by:
#   - set_default (below)
############################################################

error() {
    echo "$1" > /dev/stderr
    exit 1
}








############################################################
# return_value
############################################################
#
# Just echo — the "return" of a function whose caller
# captures stdout with $(...). Kept so set_default reads as
# a function with a result; nothing else uses it.
#
# Used by:
#   - set_default (below)
############################################################

return_value() {
    echo "$1"
}








############################################################
# set_default
############################################################
#
# $1 = current value, $2 = default. Docker hands a compose
# `- FOO` line with no value over as the literal two
# characters "" rather than as empty, so both the empty and
# the '""' case fall back to the default. The "specify a
# default" branch is unreachable today — every call below
# passes one.
#
# Used by:
#   - NETWORK, DEBUG, CHAIN (below)
############################################################

set_default() {
    BLANK_STRING='""'

    VARIABLE="$1"
    DEFAULT="$2"

    if [[ -z "$VARIABLE" || "$VARIABLE" == "$BLANK_STRING" ]]; then
        if [ -z "$DEFAULT" ]; then
            error "You should specify a default variable"
        else
            VARIABLE="$DEFAULT"
        fi
    fi

    return_value "$VARIABLE"
}








# STEP 1: the lnd settings. LND_DEBUG is read but the
# variable is DEBUG; HOSTNAME is the container's — Docker's
# short container id unless compose sets `hostname:` — and
# is what --rpclisten binds besides localhost
# =========================================================
DEFAULT_NETWORK="mainnet"

NETWORK=$(set_default "$NETWORK" "$DEFAULT_NETWORK")
DEBUG=$(set_default "$LND_DEBUG" "debug")
CHAIN=$(set_default "$CHAIN" "bitcoin")
HOSTNAME=$(hostname)


# STEP 2: peers, fee estimator, data directory. The peer
# and fee :- defaults are lnd upstream's and only apply
# when compose leaves the variable unset — it sets both.
# LNDDIR defaults to the /lnd mount; it must match the
# volume target in compose and the healthcheck's --lnddir
# =======================================================
NEUTRINO_CONNECT=${NEUTRINO_CONNECT:-"faucet.lightning.community,btcd-mainnet.lightning.computer"}
FEE_URL=${FEE_URL:-"https://nodes.lightning.computer/fees/v1/btc"}
LNDDIR=${LNDDIR:-"/lnd"}


# STEP 3: one --neutrino.connect flag per peer. IFS applies
# to this read only; a space after a comma would end up
# inside the peer name
# =========================================================
NEUTRINO_CONNECT_FLAGS=()
IFS=',' read -ra ADDR <<< "$NEUTRINO_CONNECT"
for NODE in "${ADDR[@]}"; do
    NEUTRINO_CONNECT_FLAGS+=(--neutrino.connect="$NODE")
done


# STEP 4: replace the shell with lnd, so PID 1 is lnd and
# docker stop's SIGTERM reaches it directly. --lnddir is
# where the wallet, tls.cert, macaroons, logs and the graph
# live — the only writable place on the read-only root FS.
# RPC listens on
# the container hostname AND localhost — the compose
# healthcheck's lncli uses localhost and is the only RPC
# client today. tlsextradomain puts LNDHOST into the cert's
# SANs so a client on the compose network could verify it
# under that name. "$@" is whatever compose `command:` adds
# =========================================================
exec lnd \
    "--lnddir=$LNDDIR" \
    --noseedbackup \
    "--$CHAIN.active" \
    "--$CHAIN.$NETWORK" \
    "--$CHAIN.node=neutrino" \
    "${NEUTRINO_CONNECT_FLAGS[@]}" \
    "--fee.url=$FEE_URL" \
    "--rpclisten=$HOSTNAME:10009" \
    "--rpclisten=localhost:10009" \
    --tlsextradomain="$LNDHOST" \
    --debuglevel="$DEBUG" \
    "$@"
