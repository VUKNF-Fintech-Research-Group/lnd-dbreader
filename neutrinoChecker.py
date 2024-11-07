import socket
import struct
import time
import hashlib
import random

def create_message(command, payload):
    magic = 0xd9b4bef9  # Mainnet magic bytes
    command = command.encode('ascii') + b'\x00' * (12 - len(command))
    length = len(payload)
    checksum = hashlib.sha256(hashlib.sha256(payload).digest()).digest()[:4]
    return struct.pack('<I12sI4s', magic, command, length, checksum) + payload

def create_version_message(addr_recv):
    version = 70015
    services = 0
    timestamp = int(time.time())
    addr_recv = addr_recv
    addr_from = b'\x00' * 26
    nonce = random.randint(0, 2**64)
    user_agent = b'/NeutrinoChecker:0.1/'
    start_height = 0
    relay = False

    payload = struct.pack('<IQQ26s26sQ', version, services, timestamp, addr_recv, addr_from, nonce)
    payload += struct.pack('<B', len(user_agent)) + user_agent
    payload += struct.pack('<I?', start_height, relay)
    return payload

def check_neutrino_support(node_ip, node_port):
    try:
        # Connect to the node
        s = socket.create_connection((node_ip, node_port), timeout=10)
        
        # Prepare and send version message
        addr_recv = b'\x00' * 10 + b'\xff\xff' + socket.inet_aton(node_ip) + struct.pack('>H', node_port)
        version_payload = create_version_message(addr_recv)
        version_msg = create_message('version', version_payload)
        s.sendall(version_msg)
        
        # Wait for response
        while True:
            header = s.recv(24)
            if not header:
                print(f"{node_ip}:{node_port} closed the connection")
                return False
            
            magic, command, length, _ = struct.unpack('<I12sI4s', header)
            command = command.strip(b'\x00').decode('ascii')
            
            if command == 'version':
                payload = s.recv(length)
                services, = struct.unpack('<Q', payload[4:12])
                supports_neutrino = bool(services & (1 << 6))  # Check if bit 6 is set
                if supports_neutrino:
                    print(f"{node_ip}:{node_port} supports Neutrino (NODE_COMPACT_FILTERS)")
                else:
                    print(f"{node_ip}:{node_port} does not support Neutrino")
                return supports_neutrino
            elif command == 'verack':
                continue
            else:
                print(f"Unexpected message from {node_ip}:{node_port}: {command}")
                return False
    except Exception as e:
        print(f"Error checking {node_ip}:{node_port}: {str(e)}")
        return False
    finally:
        s.close()

# List of nodes to check
nodes = [
    ("185.70.43.194", 8333),
    ("74.83.197.138", 8333),
    ("76.109.53.20", 8333),
    ("79.156.138.107", 8333),
    ("46.39.167.49", 8333),
    ("82.66.10.11", 8333),
    ("81.205.54.6", 8333),
    ("203.11.72.128", 8333),
    ("79.125.50.156", 8333),
]

print("Checking Neutrino support for nodes...")

for node_ip, node_port in nodes:
    check_neutrino_support(node_ip, node_port)
    print()  # Add a blank line between node checks

print("Node checking completed.")