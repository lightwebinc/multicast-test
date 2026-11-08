#!/usr/bin/env python3
"""Write the fabric-only profile manifest the joined qualification reads.

    record-profile.py OUT WORK

Everything is read from the running containers and the work directory: the
SHA-256 of the configuration file Broadcaster's propagation service mounts (the
file itself carries the callback token, so only its digest is recorded), its
datahub_discovery value and datahub URLs, whether Broadcaster's p2p client is
running, Merkle Service's libp2p settings, and the image of every component in
the path with its local image identifier.
"""
import hashlib
import json
import re
import subprocess
import sys

out, work = sys.argv[1], sys.argv[2]


def docker(*args):
    return subprocess.run(["sudo", "-n", "docker", *args], check=True, capture_output=True, text=True).stdout.strip()


def container(project, service):
    ids = docker("ps", "-a", "-q", "--filter", f"label=com.docker.compose.project={project}",
                 "--filter", f"label=com.docker.compose.service={service}")
    if not ids:
        sys.exit(f"record-profile: no container for {project}/{service}")
    return ids.splitlines()[0]


prop = container("broadcaster", "propagation")
mounts = json.loads(docker("inspect", prop, "--format", "{{json .Mounts}}"))
cfg_path = next((m["Source"] for m in mounts if m["Destination"] == "/etc/bsv-broadcaster/config.yaml"), None)
if not cfg_path:
    sys.exit("record-profile: propagation mounts no configuration file")
cfg = open(cfg_path, "rb").read()
text = cfg.decode()
disc = re.search(r"^\s*datahub_discovery:\s*(\S+)\s*$", text, re.M)
urls = re.search(r"^datahub_urls:\n((?:\s+-\s+\S+\n)+)", text, re.M)
if not disc or disc.group(1) not in ("true", "false") or not urls:
    sys.exit("record-profile: datahub settings not found in the mounted configuration")

p2p = container("broadcaster", "p2p-client")
merkle = container("broadcaster", "merkle-service")
env = dict(e.split("=", 1) for e in json.loads(docker("inspect", merkle, "--format", "{{json .Config.Env}}")) if "=" in e)

components = {}
for name, (project, service) in {
    "teranode": ("teranode-quickstart", "blockchain"),
    "bsv-broadcaster": ("broadcaster", "propagation"),
    "merkle-service": ("broadcaster", "merkle-service"),
    "shard-proxy": ("tpnil-fabric", "shard-proxy"),
    "retry-endpoint": ("tpnil-fabric", "retry-endpoint"),
    "shard-listener": ("tpnil-fabric", "shard-listener"),
    "teranode-bridge": ("tpnil-fabric", "teranode-bridge"),
    "arcade-bridge": ("tpnil-fabric", "arcade-bridge"),
}.items():
    c = container(project, service)
    components[name] = docker("inspect", c, "--format", "{{.Config.Image}} {{.Image}}")
for name, path in {"teranode-quickstart": "teranode-quickstart", "bsv-broadcaster-compose": "broadcaster"}.items():
    components[name] = subprocess.run(["git", "-C", f"{work}/{path}", "rev-parse", "HEAD"],
                                      check=True, capture_output=True, text=True).stdout.strip()

profile = {
    "schema": "lightweb-fabric-only-profile@1.0.0",
    "proofPath": "fabric",
    "components": components,
    "arcade": {
        "datahubDiscovery": disc.group(1) == "true",
        "datahubURLs": re.findall(r"-\s+(\S+)", urls.group(1)),
        "configSha256": hashlib.sha256(cfg).hexdigest(),
        "p2pClientRunning": docker("inspect", p2p, "--format", "{{.State.Running}}") == "true",
    },
    "merkleService": {
        "bootstrapPeers": env.get("P2P_BOOTSTRAP_PEERS", ""),
        "dhtMode": env.get("P2P_DHT_MODE", ""),
        "mdns": env.get("P2P_ENABLE_MDNS", "") == "true",
    },
}
with open(out, "w") as f:
    json.dump(profile, f, indent=2, sort_keys=True)
    f.write("\n")
print(json.dumps(profile["arcade"]), json.dumps(profile["merkleService"]))
