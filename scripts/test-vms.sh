#!/bin/bash
# Spin up tiny cirros VMs shaped like the homelab Talos cluster (one "cp", N
# "worker"s with a second raw disk, on their own NAT network) so lazyvirt has
# something real to show. Everything is named lazyvirt-test-* and lives in its
# own pool/network, so `down` cannot touch anything else.
#
#   scripts/test-vms.sh up [WORKERS]   # default 2 workers
#   scripts/test-vms.sh down
set -euo pipefail

export LIBVIRT_DEFAULT_URI=qemu:///system
P=lazyvirt-test
POOL_DIR=/var/lib/libvirt/images/$P
NET_CIDR_PREFIX=192.168.160
CIRROS=0.6.2
IMG_URL=https://download.cirros-cloud.net/$CIRROS/cirros-$CIRROS-x86_64-disk.img
CACHE=${XDG_CACHE_HOME:-$HOME/.cache}/lazyvirt/cirros-$CIRROS.img

up() {
  local workers=${1:-2}
  mkdir -p "$(dirname "$CACHE")"
  [[ -f $CACHE ]] || curl -fL --progress-bar -o "$CACHE" "$IMG_URL"

  if ! virsh net-info $P-net &> /dev/null; then
    virsh net-define /dev/stdin <<XML
<network>
  <name>$P-net</name>
  <forward mode='nat'/>
  <ip address='$NET_CIDR_PREFIX.1' netmask='255.255.255.0'>
    <dhcp><range start='$NET_CIDR_PREFIX.100' end='$NET_CIDR_PREFIX.200'/></dhcp>
  </ip>
</network>
XML
  fi
  virsh net-start $P-net 2> /dev/null || true

  if ! virsh pool-info $P &> /dev/null; then
    virsh pool-define-as $P dir --target "$POOL_DIR"
    virsh pool-build $P
  fi
  virsh pool-start $P 2> /dev/null || true

  # Base image uploaded once through libvirt, so qemu (not us) owns the file.
  if ! virsh vol-info base.qcow2 --pool $P &> /dev/null; then
    virsh vol-create-as $P base.qcow2 "$(stat -c %s "$CACHE")" --format qcow2
    virsh vol-upload base.qcow2 "$CACHE" --pool $P
  fi

  local names=("$P-cp")
  for ((i = 0; i < workers; i++)); do names+=("$P-worker-$i"); done

  for name in "${names[@]}"; do
    if virsh dominfo "$name" &> /dev/null; then
      echo "$name exists"
      continue
    fi
    virsh vol-create-as $P "$name-boot.qcow2" 1G --format qcow2 \
      --backing-vol base.qcow2 --backing-vol-format qcow2 > /dev/null
    local disks=(--disk "vol=$P/$name-boot.qcow2,bus=virtio")
    if [[ $name == *worker* ]]; then
      virsh vol-create-as $P "$name-longhorn.raw" 256M --format raw > /dev/null
      disks+=(--disk "vol=$P/$name-longhorn.raw,bus=virtio")
    fi
    virt-install --name "$name" --memory 128 --vcpus 1 --import \
      "${disks[@]}" --network network=$P-net,model=virtio \
      --osinfo linux2022 --graphics none --noautoconsole > /dev/null
    echo "$name started"
  done
}

down() {
  for name in $(virsh list --all --name | grep "^$P-" || true); do
    virsh destroy "$name" &> /dev/null || true
    virsh undefine "$name" --nvram &> /dev/null || virsh undefine "$name" > /dev/null
    echo "$name removed"
  done
  if virsh pool-info $P &> /dev/null; then
    virsh pool-start $P &> /dev/null || true
    for vol in $(virsh vol-list $P --name 2> /dev/null); do
      virsh vol-delete "$vol" --pool $P > /dev/null
    done
    virsh pool-destroy $P &> /dev/null || true
    virsh pool-delete $P &> /dev/null || true
    virsh pool-undefine $P > /dev/null
  fi
  virsh net-destroy $P-net &> /dev/null || true
  virsh net-undefine $P-net &> /dev/null || true
}

case ${1:-} in
  up) up "${2:-2}" ;;
  down) down ;;
  *) echo "usage: $0 up [WORKERS] | down" >&2; exit 2 ;;
esac
