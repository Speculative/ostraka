---
name: carthage-env
version: "1"
description: Describes the Carthage sandbox environment this project runs inside. Trigger this skill at the start of any session where the `$CARTHAGE` environment variable is set, or when the user asks "am I in a sandbox", "what can I do here", "is there sudo", "what's the network setup", "can I push from here", or any orientation question about this environment.
---

# Carthage sandbox orientation

If the `$CARTHAGE` environment variable is set, you are running inside a
Carthage sandbox container. Read `/etc/carthage/SANDBOX.md` for the rules
that apply to this environment — scoped mounts, no sudo, commit workflow,
agent permission-bypass guidance, and how to request additional services or
system packages.

Project source is mounted at `/workspace`. Agent auth/session state is
mounted according to the user's personal Carthage agent config, not this
project's compose file, so do not add agent-specific auth mounts to
`.carthage/docker-compose.yaml`. Note in particular that the host LAN is
*not* firewalled off; the container can reach anything the host user can
reach.

If `$CARTHAGE` is unset, this skill is a no-op. You are on the host, and
the sandbox rules don't apply.
