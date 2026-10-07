#!/bin/sh
set -eu
# This is a node-provisioning job, never an agent-command runtime. The only
# writable host paths are this application's dedicated seccomp directory,
# this named AppArmor policy file, and the kernel AppArmor loading interface.
test -f /profiles/agentserver-bwrap-v1.seccomp.json
test -f /profiles/agentserver-bwrap-v1.apparmor
test -d /host-seccomp
test -f /host-apparmor-profile
install -m 0644 /profiles/agentserver-bwrap-v1.seccomp.json /host-seccomp/bwrap-v1.json
apparmor_parser --apparmorfs /host-apparmor-kernel -Kr /profiles/agentserver-bwrap-v1.apparmor
# Persist only after the parser/kernel accepted the profile, for host reboot.
cp /profiles/agentserver-bwrap-v1.apparmor /host-apparmor-profile
chmod 0644 /host-apparmor-profile
printf '%s\n' 'AgentServer bubblewrap v1 node profiles installed'
