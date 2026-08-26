############################################################
#  [*] neutrinoChecker — do the bitcoin peers serve BIP157?
#
#  Run by hand on the host (stdlib only, no venv) before
#  editing NEUTRINO_CONNECT in docker-compose.yml. The LND
#  node runs neutrino and talks ONLY to the peers in that
#  list, so every one of them must advertise
#  NODE_COMPACT_FILTERS (service bit 6, BIP157) — otherwise
#  the node never syncs and autoheal keeps restarting it.
#
#  The check is a minimal bitcoin P2P handshake: connect,
#  send our `version`, read the peer's `version` and look
#  at its services bits. Advertising the bit is all that
#  is proven — whether the peer actually answers filter
#  requests is not tested. Candidates come from
#  https://bitnodes.io/nodes/?q=COMPACT.
#
#  Exit code 0 only when EVERY peer passed — unreachable,
#  refusing or bit-less peers all print a verdict, are
#  counted, and make the script exit 1 at the end. Lines
#  are tagged [*] info, [+] pass, [-] no bit, [!] error;
#  on a terminal the verdict tags are coloured, nothing
#  else is. Piped output and NO_COLOR get plain text.
############################################################


import os
import socket
import struct
import sys
import time
import hashlib
import random






# Mainnet magic — first 4 bytes of every P2P message; a
# peer answering with anything else is not a mainnet node
MAINNET_MAGIC = 0xd9b4bef9

# ANSI colours for the verdict tags only, and only when
# stdout is a terminal and the NO_COLOR convention is not
# set — piping into a file or grep must stay plain
USE_COLOR = sys.stdout.isatty() and not os.getenv('NO_COLOR')
GREEN = '\033[32m' if USE_COLOR else ''
YELLOW = '\033[33m' if USE_COLOR else ''
RED = '\033[31m' if USE_COLOR else ''
RESET = '\033[0m' if USE_COLOR else ''

# The peers to test — keep this list identical to
# NEUTRINO_CONNECT in docker-compose.yml, it is the same
# three addresses
nodes = [
    ("185.70.43.194", 8333),
    ("167.235.9.82", 8333),
    ("176.9.150.253", 8333),
]








############################################################
# paint
############################################################
#
# `[tag] message` with ONLY the tag coloured — the verdict
# at a glance, the text itself plain: [+] pass, [-] the
# peer is up but has no filters, [!] error. Info lines
# ([*]) do not go through here and carry no colour.
#
# Used by:
#   - check_neutrino_support, main flow (below)
############################################################

def paint(color, tag, message):
    return f"{color}[{tag}]{RESET} {message}"








############################################################
# create_message
############################################################
#
# Frames a payload as a bitcoin P2P message: mainnet magic,
# the command name null-padded to 12 bytes, payload length
# and the first 4 bytes of the double-SHA256 checksum, all
# little-endian, then the payload.
#
# Used by:
#   - check_neutrino_support (below) — the version message
############################################################

def create_message(command, payload):
    command = command.encode('ascii') + b'\x00' * (12 - len(command))
    length = len(payload)
    checksum = hashlib.sha256(hashlib.sha256(payload).digest()).digest()[:4]
    return struct.pack('<I12sI4s', MAINNET_MAGIC, command, length, checksum) + payload








############################################################
# create_version_message
############################################################
#
# The `version` payload we announce: protocol 70015 (old
# enough that peers do not follow up with wtxidrelay or
# sendaddrv2 before verack), no services, relay=False, a
# NeutrinoChecker user agent, height 0. addr_recv is the
# 26-byte net_addr the caller built (8 bytes services, 16
# bytes IPv6-mapped address, 2 bytes port big-endian);
# addr_from is left all zeros, as bitcoin core does.
#
# Used by:
#   - check_neutrino_support (below)
############################################################

def create_version_message(addr_recv):
    version = 70015
    services = 0
    timestamp = int(time.time())
    addr_from = b'\x00' * 26
    nonce = random.randint(0, 2**64 - 1)
    user_agent = b'/NeutrinoChecker:0.1/'
    start_height = 0
    relay = False

    payload = struct.pack('<IQQ26s26sQ', version, services, timestamp, addr_recv, addr_from, nonce)
    payload += struct.pack('<B', len(user_agent)) + user_agent
    payload += struct.pack('<I?', start_height, relay)
    return payload








############################################################
# recv_exact
############################################################
#
# Reads exactly n bytes, looping over recv — TCP is free to
# hand a 24-byte header over in two pieces, and a version
# payload in several. Raises ConnectionError when the peer
# closes the connection before n bytes arrived, so the
# caller's except reports it like any other failure.
#
# Used by:
#   - check_neutrino_support (below) — header and payload
############################################################

def recv_exact(sock, n):
    data = b''
    while len(data) < n:
        chunk = sock.recv(n - len(data))
        if not chunk:
            raise ConnectionError("peer closed the connection")
        data += chunk
    return data








############################################################
# check_neutrino_support
############################################################
#
# One peer: connect (10 s timeout), send version, read
# messages until the peer's version arrives, report bit 6
# of its services. Prints the verdict and returns it; every
# failure — unreachable, refused, closed early, wrong magic,
# unexpected message — prints and returns False. `s` starts
# as None so the finally block can close only a socket that
# was actually opened.
#
# Used by:
#   - main flow (below)
############################################################

def check_neutrino_support(node_ip, node_port):
    s = None
    try:
        # STEP 1: connect and send our version. addr_recv is a
        # 26-byte net_addr: 8 zero bytes of services, the IPv4
        # as an IPv6-mapped address, the port big-endian
        # ====================================================
        s = socket.create_connection((node_ip, node_port), timeout=10)
        
        addr_recv = b'\x00' * 18 + b'\xff\xff' + socket.inet_aton(node_ip) + struct.pack('>H', node_port)
        version_payload = create_version_message(addr_recv)
        version_msg = create_message('version', version_payload)
        s.sendall(version_msg)
        

        # STEP 2: read until the peer's version. Its verack may
        # arrive first and is skipped; a foreign magic or any
        # other message ends the check as unexpected
        # =====================================================
        while True:
            header = recv_exact(s, 24)
            magic, command, length, _ = struct.unpack('<I12sI4s', header)
            command = command.strip(b'\x00').decode('ascii')
            if magic != MAINNET_MAGIC:
                print(paint(RED, '!', f"{node_ip}:{node_port} — unexpected magic {magic:#010x}, not a mainnet node"))
                return False
            
            if command == 'version':
                # services is the 8 bytes after the 4-byte
                # protocol version; bit 6 = NODE_COMPACT_FILTERS
                payload = recv_exact(s, length)
                services, = struct.unpack('<Q', payload[4:12])
                supports_neutrino = bool(services & (1 << 6))
                if supports_neutrino:
                    print(paint(GREEN, '+', f"{node_ip}:{node_port} — supports Neutrino (NODE_COMPACT_FILTERS)"))
                else:
                    print(paint(YELLOW, '-', f"{node_ip}:{node_port} — reachable, but does NOT advertise NODE_COMPACT_FILTERS"))
                return supports_neutrino
            elif command == 'verack':
                continue
            else:
                print(paint(RED, '!', f"{node_ip}:{node_port} — unexpected message before version: {command}"))
                return False
    except Exception as e:
        print(paint(RED, '!', f"{node_ip}:{node_port} — {str(e)}"))
        return False
    finally:
        if s:
            s.close()








############################################################
# main flow
############################################################
#
# No __main__ guard — this runs on import, which is fine
# for a hand-run script. One verdict line per peer in
# `nodes`, a blank line, then the summary. Exit 1 when any
# peer failed, so the script can gate a compose edit in a
# shell one-liner.
#
# Used by:
#   - a human, by hand: python3 neutrinoChecker.py
############################################################

print(f"[*] Checking Neutrino support for {len(nodes)} peers...\n")

failed = []
for node_ip, node_port in nodes:
    if not check_neutrino_support(node_ip, node_port):
        failed.append(f"{node_ip}:{node_port}")
print()

if failed:
    print(paint(RED, '!', f"Done: {len(failed)} of {len(nodes)} peers FAILED — {', '.join(failed)}"))
    sys.exit(1)
print(f"[*] Done: all {len(nodes)} peers support Neutrino.")
