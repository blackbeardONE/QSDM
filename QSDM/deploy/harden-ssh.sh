#!/usr/bin/env bash
# Safely harden sshd: key-only auth, no root password.
# Strategy: install the drop-in, validate config, reload ssh (not restart) so the
# *existing* session stays up even if validation fails; only then enable the change
# permanently. Also neutralize the Ubuntu cloud-init drop-in that re-enables passwords.
set -euo pipefail

DROPIN_SRC="/root/qsdm-deploy/99-qsdm-hardening.conf"
DROPIN_DST="/etc/ssh/sshd_config.d/99-qsdm-hardening.conf"

# 1. Confirm at least one root authorized_key is present; aborting otherwise
if ! grep -qE '^(ssh-ed25519|ssh-rsa|ecdsa-sha2-|sk-ssh-ed25519)' /root/.ssh/authorized_keys 2>/dev/null; then
  echo "ABORT: /root/.ssh/authorized_keys has no valid pubkey; refusing to disable password auth" >&2
  exit 2
fi

# 2. Ubuntu 24.04 ships /etc/ssh/sshd_config.d/50-cloud-init.conf with "PasswordAuthentication yes".
#    Disable it (keep as .disabled for easy revert) so our drop-in isn't silently overridden.
if [ -f /etc/ssh/sshd_config.d/50-cloud-init.conf ] && ! [ -f /etc/ssh/sshd_config.d/50-cloud-init.conf.disabled ]; then
  mv /etc/ssh/sshd_config.d/50-cloud-init.conf /etc/ssh/sshd_config.d/50-cloud-init.conf.disabled
fi

install -m0644 "${DROPIN_SRC}" "${DROPIN_DST}"

# 3. Validate before applying
if ! sshd -t; then
  echo "sshd -t FAILED; rolling back" >&2
  rm -f "${DROPIN_DST}"
  if [ -f /etc/ssh/sshd_config.d/50-cloud-init.conf.disabled ]; then
    mv /etc/ssh/sshd_config.d/50-cloud-init.conf.disabled /etc/ssh/sshd_config.d/50-cloud-init.conf
  fi
  exit 3
fi

# 4. Reload, not restart (keeps current sessions)
systemctl reload ssh || systemctl reload sshd

# 5. Report effective settings
echo '--- Effective sshd settings ---'
sshd -T -C user=root,host=localhost,addr=127.0.0.1 \
  | grep -E '^(permitrootlogin|passwordauthentication|pubkeyauthentication|kbdinteractiveauthentication|permitemptypasswords|maxauthtries|usepam)' \
  | sort
