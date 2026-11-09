#!/usr/bin/env python3
"""Write the overlay qualification's environment record.

    record-environment.py OUT

Reads the image of every component on the overlay path from the running
containers: the two overlay hosts and their store, the header anchor, both
overlay bridges, and the fabric between them.
"""
import json
import subprocess
import sys

COMPONENTS = {
    "overlay-host-a": ("tpnil-overlay", "overlay-host-a"),
    "overlay-host-b": ("tpnil-overlay", "overlay-host-b"),
    "mongo": ("tpnil-overlay", "mongo"),
    "header-anchor": ("tpnil-overlay", "header-anchor"),
    "overlay-bridge-a": ("tpnil-overlay", "overlay-bridge-a"),
    "overlay-bridge-b": ("tpnil-overlay", "overlay-bridge-b"),
    "shard-proxy": ("tpnil-fabric", "shard-proxy"),
    "retry-endpoint": ("tpnil-fabric", "retry-endpoint"),
    "shard-listener": ("tpnil-fabric", "shard-listener"),
}


def docker(*args):
    return subprocess.run(["sudo", "-n", "docker", *args], check=True, capture_output=True, text=True).stdout.strip()


components = {}
for name, (project, service) in COMPONENTS.items():
    ids = docker("ps", "-q", "--filter", f"label=com.docker.compose.project={project}",
                 "--filter", f"label=com.docker.compose.service={service}")
    if not ids:
        sys.exit(f"record-environment: {project}/{service} is not running")
    components[name] = docker("inspect", ids.splitlines()[0], "--format", "{{.Config.Image}} {{.Image}}")
with open(sys.argv[1], "w") as f:
    json.dump({"schema": "tpnil-joined-environment@1.0.0", "components": components}, f, indent=2, sort_keys=True)
    f.write("\n")
