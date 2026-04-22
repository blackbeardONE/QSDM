#!/usr/bin/env bash
set +e
echo '===== uptime / host ====='
uptime; hostname; date -u

echo
echo '===== systemd services ====='
systemctl is-active qsdmplus caddy ssh
systemctl --no-pager --full status qsdmplus | head -n 10
echo '---'
systemctl --no-pager --full status caddy | head -n 10

echo
echo '===== listening sockets ====='
ss -tlnp | grep -E ':(22|80|443|2019|4001|8080|8081|8443)\b' | sort

echo
echo '===== ufw ====='
ufw status verbose | head -n 20

echo
echo '===== sshd policy ====='
sshd -T -C user=root,host=localhost,addr=127.0.0.1 \
  | grep -E '^(permitrootlogin|passwordauthentication|pubkeyauthentication|kbdinteractiveauthentication|permitemptypasswords|maxauthtries)' | sort

echo
echo '===== qsdmplus config (key lines) ====='
grep -E '^(transaction_interval|port|enable_tls|\[)' /opt/qsdmplus/qsdmplus.toml | head -n 40

echo
echo '===== qsdmplus recent log (tx cadence / libp2p port) ====='
journalctl -u qsdmplus -n 20 --no-pager | tail -n 20

echo
echo '===== Caddy TLS status ====='
journalctl -u caddy -n 25 --no-pager | tail -n 25

echo
echo '===== disk / mem ====='
free -h | head -n 2
df -h / | head -n 2

echo
echo '===== cron (backup) ====='
crontab -l | grep -v '^#'

echo
echo '===== HTTP probes ====='
curl -sS -o /dev/null -w 'http://127.0.0.1:8080/health -> %{http_code}\n' http://127.0.0.1:8080/health
curl -sS -o /dev/null -w 'http://127.0.0.1:8081/ -> %{http_code}\n' http://127.0.0.1:8081/
curl -sS -o /dev/null -w 'http://127.0.0.1/ -> %{http_code}\n' http://127.0.0.1/
