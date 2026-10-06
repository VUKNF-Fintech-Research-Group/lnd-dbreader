FROM lightninglabs/lnd:v0.21.4-beta

ENV LNDDIR=/lnd

COPY start-lnd.sh /start-lnd.sh

HEALTHCHECK --interval=30m --timeout=10s --start-period=15m --retries=5 \
    CMD lncli --lnddir="$LNDDIR" --rpcserver=localhost:10009 getinfo | grep -q '"synced_to_chain": true' && [ "$(lncli --lnddir="$LNDDIR" --rpcserver=localhost:10009 listpeers | grep -c '"pub_key"')" -gt 0 ]

ENTRYPOINT ["/start-lnd.sh"]
